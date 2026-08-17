# TUI Guide

Complete reference for Approach's terminal UI: panes, key bindings, and
per-view behavior. `README.md` has the short version. Related references:
config in `docs/config.md`, Flow phase semantics in `docs/flow-phases.md`,
session hooks and storage in `docs/agent-sessions.md`.

## Layout and Focus

The UI has a repos pane on the left, a stacked content column in the middle,
and shortcuts on the right. Git/Beads is the top content pane;
Sessions/Plans/Flows is the bottom pane. Approach starts with repos focused,
Beads/Ready stored above Flows, and the top pane remembered as the first content
destination. Both stored panes load for the selected repo even while repos owns
focus.

Press `enter` on a selected repo to collapse the repos pane to a narrow strip
and focus the top pane. `ctrl+r` restores and focuses the full repos pane.
Forward focus is repos → top → bottom → eligible terminal → repos; `bksp`
reverses that order. With repos collapsed, the cycle is top → bottom → eligible
terminal → top. The terminal stop is skipped when the dock is manually hidden,
automatically collapsed, empty, or has no active embedded terminal. Raw terminal
input owns Tab and Backspace;
enter terminal command mode with `ctrl+]` before using Tab to leave it. The
focused pane is highlighted with a blue border.

Each stacked pane reserves at least six list rows. If the shared content row
budget is below the 19-row split threshold, Approach degrades to one full-height
content pane: the focused pane, or the remembered content pane while repos owns
focus. Focus still moves through both stored panes and swaps which one is
visible. Active Flows and PR Babysitter are separate takeovers that always span
the combined content height and restore both stored panes exactly when closed.

Press `f2` from normal TUI views to open the prompt-template editor for the
`[agent].plan_prompt` and `[flow_prompts]` templates; embedded terminal input
focus passes F2 through to the embedded agent.

Empty panes explain why they are empty: no data for the selected repo, no
fuzzy filter matches, or a load failure with details in the status bar. Beads
uses subview-specific quiet empty/loading messages, a calm
`beads not configured` state, and persistent detailed errors as described
below.

**Destructive mode:** The app starts in read-only mode — deletion keys are
disabled. Press `D` (Shift+D) to toggle destructive mode on/off. When active,
the focused content border turns red and delete/drop hints appear in red as a
visual warning.

**Fuzzy filter:** Press `/` in the active pane to type-ahead filter repos or
content items. `enter` keeps the filter, `esc` clears it, `backspace` edits
it. Each filterable content view keeps its own filter: filtering worktrees
does not filter history, and returning to a view restores that view's previous
query. Each Beads subview likewise keeps an independent filter over bead ID,
title, and assignee; repo filtering remains available from the left pane.

## Key Reference

**Left pane (repos)**

| Key | Action |
|-----|--------|
| `↑`/`k` | Select previous repo |
| `↓`/`j` | Select next repo |
| `/` | Fuzzy filter repos |
| `A` | Choose and persist the coding agent from a picker (`codex` or `claude`) |
| `D` | Toggle destructive mode |
| `f` | Fetch all currently visible repos with `--prune` |
| `n` | Create a new local repo under the scan root, optionally creating a GitHub repo and wiring `origin` |
| `enter` | Collapse the repos pane and focus the top content pane |
| `tab` | Focus the top content pane without collapsing the repos pane |
| `T` | Attach an external terminal to the selected repo's Approach tmux session (tmux mode only) |
| `f2` | Edit prompt templates |
| `q`/`esc` | Quit |

**Content panes**

| Key | Action |
|-----|--------|
| `↑`/`k` | Move selection up |
| `↓`/`j` | Move selection down |
| `/` | Fuzzy filter the current item list, including the active Beads subview |
| Top `1` / `2` | Switch the top pane to Git / Beads at its last-used subview; Beads defaults to Ready before first use |
| Bottom `1` / `2` / `3` | Switch the bottom pane to Sessions / Plans / Flows |
| `ctrl+a` | Toggle Active Flows; pressing it again from Active Flows returns to the previous view. In tmux sessions that use `ctrl+a` as the prefix, send the prefix passthrough first. |
| `ctrl+p` | Toggle PR Babysitter; pressing it again from PR Babysitter returns to the previous view. In tmux sessions configured with `ctrl+p` as the prefix, send the prefix passthrough first. |
| `w`/`b`/`s`/`h`/`r` | Inside the Git view, switch directly to the worktrees / branches / stashes / history / reflog subview |
| `r`/`b`/`o`/`i`/`c` | Inside Beads, switch directly to the ready / blocked / open / in-progress / closed subview; the same letters keep their existing meanings outside Beads |
| `←`/`→` | Wrap between Git and Beads in the top pane, or Sessions, Plans, and Flows in the bottom pane; grouped entries use their remembered subview. Active Flows and PR Babysitter are not in either cycle. |
| `h` | Switch to the history subview inside the Git view; toggle the selected Flow's persisted headless/interactive preference on any Flow surface |
| `M` | Choose the global CLI model on a Flow row, or the selected expanded phase's model override/fallback in flows view |
| `E` | Choose the global CLI effort on a Flow row, or the selected expanded phase's effort override/fallback in flows view |
| `enter` | Page diff in `less` (dirty worktree, dirty branch, stash, commit, or reflog entry), page a selected bead's detail, resume an inline worktree session, page a session transcript, or expand/collapse plan or Flow phases |
| `g` | Launch the next launchable phase for the selected Flow in flows view |
| `s` | Start the selected CLI agent in the selected Flow's exact existing worktree on any Flow surface; page the selected summary in Sessions |
| `R` | Launch an embedded repair agent for a genuinely stalled selected Flow on any Flow surface |
| `n` | Create a new worktree in worktrees view, a new branch in branches view, or a new Flow in flows view |
| `P` | Create a review worktree from a GitHub PR number or URL |
| `N` | Create a new worktree and launch the selected coding agent |
| `m` | Move or rename a linked worktree (worktrees view), or mark the selected Flow's GitHub PR as already merged after verifying it in GitHub (eligible Flow rows) |
| `U` | Launch an agent in the selected Flow's worktree with the `[flow_prompts].autofix` prompt (default `autofix pr #<num>`), wherever `m` (mark merged) is offered and the Flow has a worktree |
| `A` | Choose and persist the global coding agent (`codex` or `claude`), or edit the selected expanded Flow phase's agent stamp |
| `a` | Launch the selected coding agent in the selected worktree, launch the selected plan or plan phase, toggle auto mode for the selected Flow, or toggle persisted auto-progression for a selected epic when `a: auto on/off` is shown |
| `d` | Delete worktree/branch, drop stash, or delete Flow data — requires destructive mode |
| `p` | Prune stale worktree — requires destructive mode (worktrees view), or open the linked PR from any Flow surface when PR metadata exists |
| `u` | Unlock a locked worktree (worktrees view) |
| `f` | Fetch with `--prune` (worktrees and branches views), or create a parked Flow with its worktree for the selected Bead in a settled Ready subview |
| `F` | Create and immediately start the selected Bead's Flow in a focused, settled Ready subview; pull with `--ff-only` outside that owned Ready selection (including eligible worktrees and checked-out branches) |
| `t` | Open or attach to a tmux/Zellij session for the worktree |
| `T` | Attach an external terminal to the selected repo's Approach tmux session (tmux mode only); reports an error when no session exists |
| `c` | Open VSCode at worktree path outside Flow surfaces, or copy the selected Flow ID on a Flow surface |
| `C` | Close the selected Flow with a required reason, or reopen it after confirmation if it is already closed |
| `x` | Show/hide sessions for the selected worktree (worktrees view), expand/collapse plan phase rows, or recover a selected Flow phase after confirmation — reset it to ready when it is recoverable, otherwise release an unfinished session that is blocking it |
| `y` | Copy hash to clipboard (history/reflog view), selected agent session ID (sessions view), plan Markdown path (plans view), or selected Flow worktree path (flows view) |
| `r` | Resume selected agent session (sessions view; CLI agents embed in-pane) or selected attached Flow phase session (flows view) |
| `o` | Page selected session transcript (sessions view), selected plan Markdown (plans view), or linked plan body (flows view) |
| `e` | Edit selected plan Markdown (plans view) |
| `i` | Alias for plan implementation launch, or open the linked GitHub issue from any Flow surface when issue metadata exists |
| `D` | Toggle destructive mode |
| `ctrl+r` | Restore and focus the full repos pane (outside search or embedded-terminal input focus) |
| `f5` | Rescan repositories and refetch both stored panes; independently supersede and refresh the visible Active Flows or PR Babysitter takeover too |
| `ctrl+t` | Hide or show the shared embedded terminal dock (outside search and terminal input); when input is focused, use `ctrl+] t` |
| `tab` | Cycle focus forward through repos, top, bottom, and an eligible terminal; collapsed repos are skipped |
| `bksp` | Cycle focus in reverse (outside search or embedded-terminal input focus) |
| `f2` | Edit prompt templates |
| `q`/`esc` | Close a prompt/dialog or quit |

## View Switching and the Header

The top header shows local `1` Git and `2` Beads, with `^a` Active Flows and
`^p` PR Babysitter pinned to the right. The bottom header independently shows
local `1` Sessions, `2` Plans, and `3` Flows. Numbers are silent no-ops from
repos, either takeover, the
other content pane, and raw terminal input.

While Git is stored in the top pane, a second header row lists its subviews with their
direct letter keys (`w` worktrees, `b` branches, `s` stashes, `h` history, `r`
reflog); the active entries are bracketed. Entering the Git view lands on the
last-used subview (worktrees on first entry), and each subview keeps its own
cursor position and filter across switches.

While Beads is active, its second header row lists `r` ready, `b` blocked, `o`
open, `i` in-progress, and `c` closed. The active top-level Beads entry and
active subview are bracketed. This extra row comes out of the list viewport,
so it does not increase the pane's outer height. Entering Beads lands on the
last-used subview (Ready before first use), and each subview keeps its own
filter, cursor, and scroll position. Use the letter keys to move directly among
Beads subviews; horizontal arrows move between the top pane's Git and Beads groups.

## Repo Pane Actions

Press `f` to run `git fetch --prune` for the currently visible repos. Repo
filtering limits the batch to the filtered list captured when the key is
pressed.

Press `n` to create a new repository directly under the resolved scan root.
The form asks for a repo name, whether to create a GitHub repo (checked by
default), and public/private visibility (public by default). Repo names must
be a single path segment: not empty, `.`, `..`, starting with `-`, containing
path separators, or ending with `-worktrees` (reserved for Approach worktree
directories). Approach always creates the local Git repository first. When GitHub
creation is enabled, Approach then runs
`gh repo create <name> --public|--private --source <path> --remote origin`;
`gh` must be installed and authenticated. If the GitHub step fails after local
creation succeeds, Approach keeps the local repo and reopens the form so
submitting again retries only the GitHub/origin setup against that existing
local path.

## Git View: Worktrees Subview (top `1`, then `w`)

Shows all worktree checkouts for the selected repo. The main (root) worktree
always appears first with a blue `[root]` annotation.

Each row shows the branch name (or `(detached)` for detached HEAD), status
indicators, and the worktree path:

- `✔` green: clean working tree
- `●` red: dirty — shows `N files +X/-Y` (lines added/deleted)
- `✗` red: stale — worktree directory no longer exists

Press `A` to choose `codex` or `claude` from a picker; Approach
persists the choice to config. Press `a` to launch the selected agent in the
current non-stale worktree, or `N` to create a worktree and launch the agent
there immediately. Press `n` to create a worktree without launching an agent.
Enter an existing branch, tag, or new branch name; Approach creates it under a
sibling `<repo>-worktrees/` directory and refreshes the list. Press `P` to
create a review worktree from a GitHub PR number or URL; Approach fetches the PR
head into `pr-<number>` and checks it out under the same sibling worktree
directory. If a matching `[bootstrap]` hook is configured, Approach runs it after
successful worktree creation; hook failures keep the worktree, show a status
error, and prevent automatic agent launch for `N`.

Press `f` to `git fetch --prune` and `F` to `git pull --ff-only` for the
selected worktree. Press `m` on a linked non-stale, unlocked worktree to move
it to an absolute path or rename it with a sibling-relative destination; dirty
local changes move with the worktree. Locked worktrees cannot be moved,
deleted, or pruned; press `u` to unlock one.

Press `x` on a selected worktree to show captured agent sessions for that
worktree inline. While the inline session list is open, `up`/`down` move
through the sessions and `enter` resumes the selected session from its
recorded `cwd` or worktree path. Filtering worktrees, refreshing the worktree
list, switching views, or changing repos closes the inline list.

An inline record cached with a Flow association is refreshed by exact provider
and session ID before routing. If it remains Flow-associated, Enter opens the
same embedded, phase-untracked, retained Flow terminal used by the other saved
session surfaces. If it is authoritatively non-Flow, the refreshed record keeps
the established external-or-tmux inline route.

## Git View: Branches Subview (`b`)

Shows non-worktree branches and the root branch. Worktree branches are managed
in the worktrees subview (`w`) and are hidden here to avoid duplication. The
root branch (checked out at the repo root) is pinned to position 0 with a blue
`[root]` annotation and cannot be deleted.

Status indicators stack on each branch:

- `✔` green: even with upstream, clean working tree
- `●` yellow: ahead/behind upstream — shows `+N/-N` counts
- `●` red: dirty worktree — shows `N files +X/-Y` (lines added/deleted)
- `●` purple: no upstream or upstream gone
- `merged` cyan: branch is fully merged into the cleanup branch (`main` or `master`)

Branches ahead of upstream show up to 5 unpushed commit messages, with
overflow count. Press `n` to create a new branch from the selected branch,
without checking it out or creating a worktree. When the root branch is dirty,
`enter` opens the diff in `less -R`. `t` opens or attaches to a tmux/Zellij
session, `c` opens VSCode at the worktree path, and `a` launches the selected
coding agent for checked-out branch rows only. `f` runs `git fetch --prune`,
and `F` runs `git pull --ff-only` for branches that have a checked-out
worktree. `d` deletes non-worktree branches, with a force-retry prompt on
failure; deletion requires destructive mode (`D`).

## Git View: Stashes Subview (`s`)

Browse stashes for the selected repo. Long stash messages wrap to two lines
(date + message start, then indented continuation). Use `↑`/`↓` to select a
stash, `enter` to page its diff in `less -R`, `d` to drop the selected stash
(with confirmation, requires destructive mode). The stash list scrolls when
entries exceed the pane height.

## Git View: History Subview (`h`)

Browse recent commits (up to 50) for the selected repo. Each row shows the
commit hash, author, relative date, and subject. Use `enter` to page the full
commit diff in `less -R`, `y` to copy the commit hash to clipboard, `t` to
open or attach to a tmux/Zellij session, and `c` to open VSCode at the repo
root.

## Git View: Reflog Subview (`r`)

Browse HEAD reflog entries (up to 50) for the selected repo. Each row shows
the abbreviated hash, selector (e.g. `HEAD@{0}`), relative date, and subject.
Use `enter` to page the diff for that entry in `less -R` — checkout entries
with no tree changes page "No changes at this reflog entry". Use `y` to copy
the entry hash to clipboard.

## Sessions View (bottom `1`)

Browse captured Claude Code and Codex sessions associated with the selected
repo. Rows show provider, branch, worktree, status, and summary. Use `/` to
filter sessions by provider, session ID, launch ID, branch, worktree, model,
status, or summary. Press `o` to page the normalized transcript in `less -R`,
`s` to page the selected summary, `r` to resume the selected provider session,
or `y` to copy the raw provider session ID. `enter` also pages the selected
transcript.

If the selected row is cached as Flow-associated, `r` first refreshes that
exact provider/session record and routes it through the Flow launch lifecycle.
An authoritative Flow resume is interactive, embedded-only, phase-untracked
(empty phase ID), nondetachable, and retained as exact-Flow occupancy after
exit. If refresh removes the Flow association, the refreshed record follows
the ordinary Sessions-pane route instead. The same is true when the referenced
Flow was deleted, because saved sessions outlive Flow records. Rows initially
without a Flow keep their existing embedded/tmux/external behavior.

How sessions are captured, where they are stored, and how resume picks its
working directory are covered in `docs/agent-sessions.md`.

## Embedded Terminals

Resuming a CLI `codex` or `claude` session, launching a CLI Flow phase, or
repairing a stalled Flow opens
a runtime-only terminal in one shared full-width dock — a top-level pane below
the repo, content, and shortcut panes, directly above the status bar.
The dock persists while switching among Git, sessions, plans, flows, Active
Flows, and PR Babysitter. Its terminal numbers and active selection are
global, so a session resume and a Flow launch appear in the same numbered
header. The current list remains usable above the dock; in particular, the
saved-session table is never replaced by the terminal surface.

Press `ctrl+t` from repo or list focus to store whether the dock is requested
open or manually hidden. An expanded dock shrinks before either stored pane is
sacrificed. If the viewport cannot fit both panes plus a framed terminal header
and one output row, the requested-open dock automatically becomes the collapsed
chip. Growing the viewport restores it automatically while the request remains
open. Pressing `ctrl+t` during automatic collapse changes that request to a
manual hide, so later growth does not restore the dock until `ctrl+t` is pressed
again.

The narrow-safe chip distinguishes `auto-collapsed · ctrl+t hide` from the
manually hidden `ctrl+t show` state when space permits; with no terminals it
reads `no terminals running`. From visible terminal input, `ctrl+t` is sent to
the PTY; use `ctrl+] t` to hide the dock. Manual or automatic collapse returns
terminal focus to the current list without stopping the process. Live PTYs are
resized to a backend-safe one-row height while hidden or automatically collapsed
and return to the allocated expanded height when shown or restored.

Tabbing from a content list into terminal focus enters terminal command mode.
Commands remain in Approach until `i` returns to terminal input mode; in input
mode, keys—including Tab, Backspace, and agent shortcuts such as `ctrl+g`—pass
through to the PTY. `ctrl+]` re-enters command mode. The command-mode keys are
the same in every view:

| Command | Action |
|---------|--------|
| `1`–`9` | Switch terminals |
| `left` / `right` | Cycle terminals with wrap |
| `i` | Return to terminal input mode |
| `t` | Hide the terminal dock |
| `l` | Open a saved-session picker for the selected repo, loading its sessions on demand when the sessions view has not populated them |
| `d` | Detach a tmux-backed terminal and open a new external terminal attached to that tmux session |
| `x` | Dismiss an exited terminal or confirm termination of a running one |
| `q` / `esc` | Quit with cleanup |
| `ctrl+]` | Send a literal `ctrl+]` to the agent |

When `tmux` is available at launch time, embedded CLI terminals start inside a
per-launch tmux session so detach can close Approach's embedded client while the
agent keeps running in tmux. The external-terminal handoff order after detach
is documented in `docs/config.md` (terminal settings). If no external terminal
transport is available, Approach still leaves the agent detached in tmux and
reports the handoff error. If `tmux` is unavailable, Approach keeps the direct
embedded PTY behavior and `ctrl+] d` reports that detach is unavailable.

Quitting Approach from anywhere while embedded terminals are still running asks
for confirmation and terminates them first. Terminate/quit cleanup kills tmux
sessions created by that embedded launch; detached tmux sessions are no longer
owned by Approach and are not prompted for on quit. Embedded terminals are not
restored after Approach restarts.

The global `ctrl+] l` picker applies the same cached-association rule as
Sessions-pane `r` and the inline worktree list. All three submit one exact
provider/session-ID lifecycle intent for cached Flow records. An authoritative
Flow-associated CLI resume always installs an embedded, phase-untracked,
nondetachable retained slot; unsupported embedded startup does not fall back.
An authoritative non-Flow refresh returns to the origin's established route.

## tmux Mode

Setting `[launch].backend = "tmux"` (see `docs/config.md`) moves interactive
CLI agent launches out of the embedded dock and into a per-repo tmux session on
your default tmux server. The embedded terminal is the default and is unchanged
unless you opt in.

What changes in tmux mode:

- **Flow phase launches (`g`), Flow phase resumes (`r`), and non-Flow
  Sessions-view/picker resumes** run as windows in the repo's tmux session
  instead of the embedded dock. Cached Flow-associated saved sessions first
  refresh through the lifecycle; if still Flow-associated they stay embedded,
  while an authoritative non-Flow record uses this tmux route. AutoMode is not
  in this list: it always launches headless, and
  headless stays embedded (see below). **A Flow's headless preference is on by
  default**, so `g` on a freshly created Flow stays embedded until you turn
  headless off with `h`; resumes (`r`) are always interactive and go to tmux
  either way, so the same phase can launch embedded and resume in tmux.
- **Worktree `a`, `N` (new worktree + agent), plans-mode implement (`a` / `i`),
  and non-Flow worktree session resume (`x` then `enter`)** run there too. A
  worktree session that remains Flow-associated after exact refresh stays in
  the embedded dock. For the other launches, this is a behavior change to
  launches that today open a *new external terminal* attached
  to a per-worktree multiplexer session — or, if you are running Approach inside
  tmux, switch your current client to that session. In tmux mode they open a
  window in the repo's session and never switch your client, so use `T` or your
  own tmux keys to get there. If you rely on the old workflow, keep the default
  backend; it is untouched.
- Every launch reports the window, the session, and the exact
  `tmux attach -t <session>` command in the status line.
- Press `T` from the repos pane or any content pane to open your configured
  external terminal attached to the selected repo's session — or, on a Flow
  surface, the selected Flow's repo, since active flows span repos. When no
  session exists Approach says so rather than creating an empty one. While an
  embedded terminal holds input focus it takes every key first, so `T` types
  into that agent instead; leave the dock's input focus first.

What does not change:

- **Flow repair (`R`)** stays embedded: repair sessions are phase-untracked and
  their obstruction/recovery contract assumes the embedded slot.
- **Headless launches** (Flow-level `h`, and therefore every AutoMode launch,
  which is always headless) stay embedded. `claude --print` buffers all output
  until it exits, so a self-closing tmux window would render nothing and then
  discard it.
- **Creation-time plan launches** stay embedded for CLI agents. New-Flow Plan
  Now and Ready `F` both reach the dock through the source-aware `createPhase`
  lifecycle, even when `[launch].backend = "tmux"`.

Lifecycle and ownership:

- Sessions are named `approach-<repo-dir>-<hash>` and are visible to your own
  `tmux ls`. Approach creates one on the first launch for a repo and reuses it
  after that.
- Quitting Approach does not prompt about, terminate, or otherwise touch an
  established session — persisting past the TUI is the point. While a tracked
  Flow's private tmux start handshake is still pending, keyboard quit is
  temporarily blocked and SIGINT/SIGTERM shutdown is deferred so the TUI cannot
  drop its launch reservation before the runner owns the Flow lease. Reattach
  established sessions with `T` or `tmux attach`.
- A window closes when its agent exits. The session ends with its last window
  and the next launch recreates it.
- A tracked Flow phase window owns a Flow-scoped kernel lease for as long as
  its agent process remains alive. Finishing the phase does not release that
  ownership: `g`, tracked `r`, and AutoMode defer the successor even after a
  completed phase reaches its CLI prompt. The same lease is visible after a TUI
  restart and to other Approach processes sharing the artifact root.
- Exiting the agent or killing its window releases the lease automatically.
  Manual launch can retry immediately; AutoMode retries on a later one-second
  poll. The stable unlocked `.lock` file is normal and does not occupy the Flow.
- If `tmux` is missing, launches fall back to their default-backend route and
  the status line says `tmux unavailable`. The availability check never refuses
  a launch; a tmux launch that then fails to spawn fails like any other launch
  error rather than retrying somewhere else.
- Resetting a phase (`x`), resuming one again (`r`), and repairing a Flow (`R`)
  ask tmux whether a window is still open for it, and refuse while one is. See
  Limitations.

Limitations:

- **Your tmux config applies.** That is mostly desirable (your prefix, your
  status bar), but `remain-on-exit on` keeps dead windows, `exit-empty off`
  keeps empty sessions, and `destroy-unattached on` can kill sessions this
  feature promises will persist. Approach does not override your config.
- Removing a worktree while a tmux window is still `cd`'d into it is not
  fenced. Worktree removal is already destructive-gated; close the window
  first.
- **The reset/resume/repair live-agent guard is a probe, not a slot.** Flow
  successor admission uses the cheap kernel lease above and never invokes tmux
  from rendering or AutoMode. The older one-shot guard remains for `x`, repeat
  `r`, `R`, and session release. In the embedded dock, a running
  slot is what stops `x` (reset), a repeat `r` (resume), and `R` (repair) from
  starting a second agent on a phase that already has one. Persisted session
  state cannot stand in for it — Claude records a session only when the agent
  exits, and Codex records an `ended` one after each turn — so in tmux mode all
  three actions instead ask tmux whether a window for that work is still open,
  and refuse with "Flow phase still has an agent running in tmux" when one is.
  `x` and `r` check the selected phase's launches; `R` checks every launch the
  Flow has made, since a repair is Flow-wide. Saved-session resumes that start
  from a session record — sessions view `r`, the inline worktree session list,
  and the dock's session picker — check the launch from the authoritative
  refreshed record and refuse with "Session still has an agent running in
  tmux". Flow-associated saved resumes repeat that check on the final record
  read under the Flow launch reservation, then launch into a lifecycle-owned
  retained dock slot; authoritatively non-Flow resumes use the configured route.
  Codex
  makes that ordinary rather than rare: it records an `ended` session after each
  turn while its CLI stays open, so the record you resume from often still has a
  live agent. Consequences worth knowing:
  - The probe runs when you confirm a reset or press `r` or `R`, not while
    rendering, so the footer may still offer `x` for a phase whose reset is then
    refused.
  - It is advisory in one direction. If tmux cannot answer (server gone,
    `list-windows` fails), the action proceeds rather than being blocked, so a
    probe failure can never strand a phase.
  - It matches windows by the launch ID that Approach put in the window name.
    Renaming a window by hand makes it invisible to the probe.
  - It only runs while `backend = "tmux"`. Windows outlive Approach, so if you
    switch back to `embedded` while one is still running, that window stops
    counting as a live agent and the guard no longer sees it. Close the windows
    before switching back.
- A window killed out of band (`tmux kill-window`, a killed agent, a machine
  sleeping) fires no provider hook, so the phase keeps whatever the last hook
  recorded: `running` and awaiting session capture if none ever fired. The probe
  then finds no window, so `x` resets it normally; use repair (`R`) if it needs
  more than a reset.
- Lease inspection fails closed. If the shared state root, lease directory, or
  lock file is unsafe or unreadable, the footer withdraws launch eligibility,
  manual launch reports the setup error, and AutoMode waits silently. Repair
  the artifact-root permissions or unsafe node and retry.
- Renaming a tracked phase window does not defeat successor admission: the
  lease is tied to the supervising process, not the window name. Out-of-band
  commands that replace the pane command or otherwise bypass Approach's private
  runner are ordinary tmux activity and do not acquire a Flow lease.

## Plans View (bottom `2`)

Browse saved agent plans for the selected repo. Rows show status, branch,
phase progress (`completed/total`), the updated date, and the title. Use `/`
to filter plans by title, summary, status, branch, worktree basename,
provider, session ID, launch ID, and phase titles/statuses. Press `x` to
expand or collapse the selected plan's phase rows, `o` to page the plan
Markdown in `less -R`, `e` to edit the plan Markdown, and `y` to copy the plan
Markdown path. The edit action opens `[editor].command` when configured,
otherwise `$EDITOR`, and refreshes the plans pane when that command exits; use
wait flags such as `code --wait` for GUI editors that detach by default. Press
`a` to edit launch instructions for the selected plan or selected phase, then
`enter` to launch the selected agent or `esc` to cancel; blank instructions
are rejected. `enter` still toggles phase rows, and `i` still opens plan
launch instructions as compatibility aliases.

Plans are persisted explicitly by agents through the `approach plan` CLI rather
than captured from hooks; the canonical agent instructions are the
`approach-plan-persist` skill (`agent-skills/approach-plan-persist/SKILL.md`).
Plans share the agent-artifact root with sessions (see
`docs/agent-sessions.md` for the storage layout); because plans live beside
sessions, moving or cleaning the sessions root also moves or removes saved
plans. v1 has no TUI plan deletion.

## Beads View (top `2`)

With the top content pane focused, press `2` to enter the selected repository's
Beads group at its last-used subview, defaulting to Ready before first use.
Beads queries and detail reads are read-only. Manual Ready Flow creation is
also claim-free; the only tracker mutation in this group is the child claim
performed when epic auto-progression prepares a new Flow. Pressing `2` while
already in any Beads subview is a no-op. Press `r` for
Ready, `b` for Blocked, `o` for Open, `i` for In-Progress, or `c` for Closed;
pressing the already-active letter is also a no-op. Top-pane `←`/`→` switches
between Git and Beads at their remembered subviews. The five Beads modes keep
independent rows, filters,
cursor/scroll positions, availability, loading state, and request tokens.

The query sources and ordering are:

- Ready: `bd ready --json --limit 0 --readonly`, sorted by priority then natural ID.
- Blocked: `bd list -s blocked --json --limit 0 --readonly`, sorted by priority then natural ID.
- Open: `bd list -s open --json --limit 0 --readonly`, sorted by priority then natural ID.
- In-Progress: `bd list -s in_progress --json --limit 0 --readonly`, sorted by priority then natural ID.
- Closed: `bd list -s closed --json --limit 100 --sort closed --reverse --readonly`, selecting the newest 100 before the result is parsed and sorted by descending `closed_at`, then natural ID. The full total comes from `bd stats --json --no-activity --readonly` at `summary.closed_issues` (or the v1 `data.summary.closed_issues` envelope).

Ready is `bd`'s dependency-graph computation, not a status derived inside
Approach. Ready and Open are independent results: an open bead with all
blockers resolved intentionally appears in both. Rows render as
`<id>  P<n>  <title>` and append two spaces plus the assignee when present.
Rows whose optional `issue_type` is `epic` (case-insensitive, ignoring outer
space) append `[epic]` in all five subviews; older `bd` output may omit both
`issue_type` and `parent` without changing existing rows.

Selecting an epic expands it inline in the stored top Beads pane, including
while the bottom pane has focus. Approach asynchronously runs exactly
`bd children <epic-id> --json --readonly` for direct children and the existing
`bd ready --json --limit 0 --readonly` query as the readiness oracle. Ready
direct children appear first in Ready's established priority/natural-ID order;
the other direct children follow in the children query's stable
priority/natural-ID order (with raw ID as the final tie-break). A child gets a
`[ready]` marker only when it is positively present in Ready. Other children
remain neutral because they may be blocked, closed, or already in progress.

The parent row is followed by a single loading, empty, or child-query-error
line, or by the ordered child rows. If children load but readiness does not,
all children remain visible in stable order without status markers and a local
`Readiness unavailable` warning follows them. These bounded local states do not
change whether the parent Beads list is available. Up/down scroll through an
expanded epic that exceeds the viewport before moving the selection away.
Changing selection or filter, clearing a filter, switching Beads subviews,
changing repos, refreshing, or leaving Beads clears the expansion and
invalidates its request synchronously. A returning result must still match the
same repository, subview, epic, and request token; switching away and back does
not reuse child data. There is no child polling.

For a settled Closed result, the active header item shows the unfiltered
accepted row count: plain `closed 0` through `closed 100` when the stats total
is not larger, or `closed 100 of <total>` when more rows exist. The two queries
are separate snapshots, so `total <= fetched` deliberately uses the plain
fetched count. Loading and unavailable results show no count, and a fuzzy
filter does not change it. Failure of either Closed query discards both rows
and total and uses the shared unavailable state.

Successful empty queries show exactly `no ready beads`, `no blocked beads`,
`no open beads`, `no in-progress beads`, or `no closed beads`. Pending queries
use the corresponding `loading ... beads` message. A missing `bd` binary or a
repository without a Beads project/database shows exactly
`beads not configured`. Other command failures and JSON parsing failures show
a persistent `Could not load <subview> beads: <detail>` error; its detail is
sanitized to one terminal-safe line and truncated to the pane width.

`/` filters only the active Beads pane by ID, title, and assignee. Subview
switches and `f5` retain that pane's same-repo rows, query, cursor, and scroll
internally while the loading UI hides old rows. A loading pane shows only its
`loading` message and ignores cursor keys, so no selection moves behind it. An
accepted replacement reapplies the filter and clamps selection and scroll for
shorter, empty, or zero-match results. An unavailable result retains the query
but clears that pane's rows and selection. Moving to another repo invalidates
every Beads request, clears all old-repo rows and cursor/scroll positions,
retains each subview's query, and starts the stored top Beads subview and the
stored bottom mode for the new repo; only that top subview becomes pending
within the Beads group. Retention is same-repo only, so this holds even when the
repo changes while Active Flows is open.
Per-mode request tokens reject results for an old repo, an older refresh, or a
subview that is no longer active. Every query is read-only, and `bd -C` plus the
selected process directory are owned by the query runner.

In Ready only, `f` and `F` belong to a focused content pane whose query is
settled and available and whose filtered visible selection has a non-empty Bead
ID. Lowercase `f` is executable whenever no Ready Flow request is in flight;
uppercase `F` additionally requires a configured launch agent. The footer and
shortcut pane advertise the actions separately as `f: new flow` and
`F: new flow + start`. An owned Ready selection consumes `F` even when the agent
is missing or either Ready action is busy, so it cannot fall through to pull;
outside that ownership context uppercase `F` keeps its normal pull binding.

Both keys asynchronously create exactly one Approach Flow in the selected repo.
The record title is
`<trimmed bead ID>: <trimmed bead title>` and its instructions are
``Use Bead <id> as the durable source of requirements. Read it with `bd show <id>` before planning or implementation.`` The configured Flow preset seeds the
phase graph and normal Flow creation defaults apply. Both shortcuts prepare the
Flow exactly like an `n` form submission with Plan Now off: it creates the
`flow/<slug>` branch and worktree from the repository's current HEAD, records
the worktree, branch, and commit start metadata, and runs the repository's
bootstrap hook. Lowercase `f` stops there: it does not link a saved plan or
GitHub Issue, start a Flow phase, or launch an agent. Both keys persist a
separate Bead link from the already-loaded row, containing the trimmed Bead ID
and its trimmed parent epic ID when available; they still do not link a saved
plan or GitHub Issue. Uppercase `F` submits a Ready-source `createPhase` intent
to the same exact-Flow launch lifecycle as Plan Now. The lifecycle selects the
first actionable phase and uses the configured agent, model, reasoning effort,
Flow prompt-template snapshot, session state root, and an explicit default-on
headless setting. Its creation-time launch is strictly embedded, even when
`[launch].backend = "tmux"`; no external launch fallback bypasses lifecycle
ownership. Neither action invokes `bd`, calls `bd show`, claims the issue, or
otherwise changes tracker state.

The two keys share one Ready admission token. Repeated or mixed presses cannot
create duplicate Flows. A repository change — cursor move or rescan —
invalidates that source's pending presentation without clearing a newer Ready
request or an unrelated Plan Now request. Before persistence, stale lifecycle
events stop without side effects. After exact creation, recovery still finishes
for that persisted Flow while status, refresh, focus, and request clearing stay
fenced to the current source token. Every post-creation error names the
persisted Flow ID, and a visible Flow surface refreshes only for the current
request.

Every Flow launch surface — Plan Now, Ready `F`, manual `g`, AutoMode, phase
resume, saved-session resume, repair, worktree agent, and autofix — is admitted
by the same exact-Flow lifecycle. This does not merge their policies: manual and
automatic launches keep their existing headless, retry, status, and tmux rules;
saved-session resume and Ready `F` remain embedded-only; occupancy is still
Flow-scoped; and the terminal dock takes ownership only after a successful
embedded install. Pane focus and key ownership remain presentation concerns and
never decide whether a launch may bypass lifecycle admission.

For a selected epic with loaded children and readiness, `a: auto on` enables
progression from the first ready direct child. After a complete Flow listing
rules out ambiguous or unusable candidates, a new-Flow path refreshes the epic's
direct children and the repository Ready set. If the selected child is no longer
both direct and ready, the attempt stops without a claim. Otherwise it persists
the receipt-less exact-link Flow identity, runs
`bd update --claim -- <child-id>`, and waits for it before creating a worktree.
The Flow instructions use `bd show -- <child-id>`. Any claim error is shown with
the child ID and underlying cause, retains the marked unprepared identity for
same-actor retry, and leaves progression known off.
Because a process-started error can leave ownership uncertain or already
claimed, Approach neither probes nor unclaims; retry uses the same actor. A
successful claim is likewise retained if later Flow preparation, reservation,
or progression enablement fails. Before choosing another ready sibling, retry
finds the claimed direct child's open marked receipt-less or prepared-pending
exact-link Flow even though that child is no longer Ready; it reserves the Flow,
revalidates its generation and current direct-child membership, repeats the
idempotent claim, then surfaces incomplete preparation or adopts the prepared
Flow. Consumed `completed`/`merged` markers are ignored so off/on recovery can
select a later Ready sibling. Unmarked manual Flows do not enter this recovery
path. This action prepares or adopts only; it does not start a phase or launch
an agent.

Enabled epics advance from the same view-independent 1 Hz Flow poll. When the
exact in-session baseline Flow is newly observed as `completed` or `merged`,
Approach prepares the next unlinked direct child in fresh `bd ready` order; it
does not claim the Bead or launch the new Flow. The status line reports the
prepared child and Flow ID, retryable preparation/reconciliation errors, or an
owned successor that blocks later children. When no creation candidate remains,
it reports auto-progression completion and turns normal progression off. No
startup catch-up occurs: explicitly toggle progression off and back on to
install a new live baseline after reconciling an unknown drain state.

Lowercase `f` produces the same parked Flow as a successful `n` form submission,
so `g` on its first phase launches the agent inside the Flow's isolated
worktree. An initial store failure leaves no Flow. If worktree creation fails,
the persisted Flow record keeps its
launchable phases blocked with the failure noted, the Flows pane renders the
worktree-less record with the `missing-worktree` branch label and a
`recover-worktree` phase state, and the error is reported in the status line. A
start-metadata failure occurs after the directory and branch exist but before
the Flow records them; it reports the persisted Flow ID without inventing a
blocked phase. A bootstrap-hook failure comes after the start metadata is
persisted, so that record does have a worktree and its launchable phases are
blocked. Reservation refusal or launch-ID persistence failure retains the
prepared Flow without spawning or leaking the reservation. Embedded-open,
prefill, external-construction, terminal-spawn, and later agent failures use the
existing launch-failure persistence and recovery behavior; the Flow remains
available and the status line shows the failure.

A Flow that has no worktree at all — one created by `approach flow create`
without `--worktree-path`, or left behind by a failure before the start metadata
was written — never launches in the repository root. Pressing `g` creates the
worktree first, announcing it in the status line, and only then starts the
agent.

A Flow that records a non-empty worktree path takes a different route: Approach
inspects that exact path before persisting a phase launch ID or preparing an
agent process. If the path is missing or is not a directory, a `g` phase launch
or `r` phase resume is refused with the recorded path and inspection reason,
while AutoMode blocks the candidate phase with the same reason in its notes.
Approach does not clear the path, prune Git metadata, recreate the worktree, or
fall back to the repository root; those cases can require different recovery
choices.

A record that already names a local branch gets a worktree for that branch, so
the name prompts render as the push target keeps meaning what it said. If that
branch already has a healthy linked worktree, the Flow adopts it rather than
failing over a checkout git will not repeat — the bootstrap hook then runs
against a directory that already exists, so a hook that scaffolds rather than
installs sees a populated worktree. A registration git marks prunable is never
adopted, whether the directory is gone or only its `.git` link is. Only a record
whose branch resolves to nothing — or that names none — is given a fresh
`flow/<slug>` pair from the recorded base ref, or from the repository's current
HEAD when no base ref was recorded.

Creating the worktree, recording it, and running the bootstrap hook all happen
under the Flow's launch reservation, so two Approach processes launching the
same worktree-less Flow serialize: the second one adopts the fully provisioned
worktree the first recorded instead of allocating a second pair beside it. That
reservation is held longer than its own lock timeout allows for, so a launch
that arrives mid-provisioning is refused for now rather than for good — the
status line says another launch is setting the worktree up, and AutoMode simply
retries on its next poll. A Flow closed while the launch was reading is dropped
without a status, the same as any other stale candidate.

A manual launch is refused with the reason, and AutoMode blocks the phase with
that reason in its notes rather than retrying every second, when:

- the Flow records no repository of its own;
- the recorded branch exists but is not a local branch — a tag, a
  remote-tracking ref, or a raw commit — since checking one out would detach
  HEAD under a record that keeps naming a branch;
- the recorded branch is checked out in the repository's own working tree,
  which is the launch this whole path exists to prevent;
- `git worktree add` fails for any other reason.

Most of those refusals write nothing, but two happen after `git worktree add`
has already run. A store that will not record the new worktree leaves a branch
and directory on disk that no record names; the status line names the path. A
bootstrap-hook failure comes after the start metadata is persisted, so the Flow
does keep the worktree it just gained, and its status says so — note that a
second `g` validates the recorded directory and launches the agent into it when
it remains usable, even though the hook never completed.

After the active subview settles, `enter` on its visible selected row
asynchronously loads the raw human-readable output of
`bd show <id> --readonly` and pages it through `less -R`. Detail results use the
same active-view request lifecycle as other read-only detail panes: a repeated
`enter`, subview or repo transition, or Beads list refresh invalidates the old
request, and delivery also requires the same bead to remain visibly selected.
Stale successes do not launch a pager and stale errors do not change status.

## Flows View (bottom `3`)

Browse persisted Flow records for the selected repo. Row columns are Status,
Branch, Phase (progress plus current phase state), Issue, Plan, PR, Updated,
Title. Use `/` to filter by title, instructions, status, branch, worktree
basename, plan metadata, issue metadata, PR metadata, phase
titles/statuses/summaries, and linked session metadata.

Press `n` to create a new Flow with one form for title, multiline instructions
(`alt+enter` for newlines), and optional base ref plus Headless and Plan Now
checkboxes. New Flows use the built-in default phase graph unless
`[flow].preset` selects a configured custom graph. Plan Now is checked by
default and immediately launches the first ready root phase after creating the
Flow; uncheck it to create a parked Flow whose ready root phase can be
launched later from the Flow row. The Headless checkbox is persisted on the
new Flow in either case, so a parked Flow uses the same choice when launched
later. Ready-Bead creation retains the default-on setting: lowercase `f`
persists it for the parked Flow, while uppercase `F` also uses it for the
immediate start. Command-line creation retains the same default.

New-Flow submissions are single-flight across both Plan Now choices. While one
submission is active, another form may be opened but cannot be submitted. With
Plan Now enabled, the form emits only creation intent. The launch lifecycle
allocates the final Flow ID before it checks exact-ID sessions; allocation is
non-durable and does not reserve or create a record, so exact-ID creation is the
collision authority. After creation the lifecycle holds its in-memory attempt
and the cross-process launch/close reservation across worktree setup, bootstrap,
launch-ID persistence, metadata, and embedded installation or recovery. Worktree,
branch, and commit metadata are persisted immediately after worktree creation,
before bootstrap or launch-ID persistence, so an interrupted bootstrap retains
the artifact identity for recovery. The first canonical launchable root starts;
parallel roots remain available unless startup recovery must block the captured
root set. A graph with no launchable root is parked after metadata without a
launch ID.

On a Flow row or an expanded phase row:

- `enter` expands or collapses the phase detail rows.
- `o` pages the linked plan body in `less -R`; Approach shows a status message
  when the selected Flow has no linked plan.
- `g` launches the first launchable phase in the selected Flow's canonical
  phase order. This uses the selected Flow, so a highlighted pending phase row
  can still launch an earlier ready sibling; nothing is persisted when no
  phase is launchable. The key press only submits a launch intent: the launch
  lifecycle reserves the Flow, re-reads the persisted record, and decides from
  it, so a launch the list advertised can still be refused once the fresh
  record disagrees. A launch is rejected — not merely unadvertised — while an
  embedded terminal or another launch, resume, or repair already holds that
  Flow, and while a live session is attached to the phase being launched.
  Dismiss or detach the Flow's terminal before launching its next phase. Both
  `g` bindings (flows view and Active Flows) behave identically.
- `s` starts the configured `codex` or `claude` CLI in the selected Flow's
  exact existing worktree from either Flow surface. The key submits only a
  `worktreeAgent` intent with the trimmed exact Flow ID. The lifecycle reads
  that exact Flow and its exact-Flow sessions, then repeats those reads and the
  worktree-directory check under the launch/close reservation immediately
  before startup. A running phase, live phase session, any active persisted
  Flow session, competing attempt, retained terminal, missing/non-directory
  worktree, command drift, or unresolved linked-plan path refuses the launch.

  This generic agent is always interactive and embedded: it ignores the
  Flow's headless preference and tmux backend, carries no prompt, and schedules
  no prompt prefill. It is Flow-scoped but phase-untracked, so it writes no
  launch ID or phase status and hook capture cannot attach it to phase history.
  Ownership transfers directly from the lifecycle attempt to a retained dock
  slot. That slot is nondetachable and keeps the exact Flow occupied even after
  the process exits, until it is closed or dismissed. Outside Flow surfaces,
  `s` keeps its existing selected-session-summary behavior.
- `R` repairs a genuinely stalled nonterminal Flow from either its top-level
  row or an expanded phase row. The shortcut is shown only when no phase can
  be launched manually, no healthy phase session is running, no Flow terminal
  slot is occupied — including a repair slot whose terminal has not started —
  no launch, resume, or repair is already in flight for that Flow, no headless
  write for that Flow is in flight, and the Flow is not completed, merged,
  abandoned, closed, or at a ready/manual Merge boundary. Like `g`, the key
  press only submits an intent: the launch lifecycle re-reads the persisted
  Flow and decides from it, so a repair the list advertised can still be
  refused. A refusal names the obstacle, preferring a durable one (a pending
  repair, a phase resume holding the Flow, an open terminal) over the transient
  write.
- `i` opens the linked GitHub issue and `p` opens the linked PR in the
  browser, when that metadata exists.
- `c` copies the selected Flow ID; `y` copies the selected Flow worktree path.
- `r` on an expanded phase row with an attached provider session resumes that
  session and records it as a fresh Flow phase launch attempt. Like `g`, the key
  press only submits an intent: the launch
  lifecycle re-reads the persisted Flow and re-checks the phase and its latest
  resumable session, so a resume the list advertised can still be refused with
  `Flow phase changed; refresh and try again` once the fresh record names a
  different session. A CLI resume is refused while any embedded terminal holds
  that Flow — including a retained slot whose agent has already exited, and
  including a terminal on another phase — with `Close, detach, or dismiss the
  existing Flow terminal before resuming this phase`; `r` is withdrawn from the
  footer for exactly that case. Any competing lifecycle launch — including a
  repair — or an open repair terminal refuses silently. A live `U` autofix
  window in tmux mode refuses the resume with `Flow still has an agent running
  in tmux`, and a live tmux window for the phase itself is also probed before
  launch. A different live session on the same phase refuses
  with `Flow phase already has a running session`; the session being resumed does
  not count against itself, which is what keeps `r` open for a phase whose
  agent died without recording an end. That exemption is keyed on the store's
  own identity — provider and session ID together — so a live session from a
  different provider still refuses even when it shares the target's ID.
- `a` toggles per-Flow auto mode, which is on by default for new Flows and
  persisted on that Flow record. Flows created before this field existed
  remain manual until toggled on.
- `h` toggles the selected Flow's persisted manual-launch preference. It works
  from the Flow row or an expanded phase row and is hidden when no Flow is
  selected; changing one Flow does not affect another. Launches read the
  preference from the store, so while the write is in flight that Flow's `g`
  is withdrawn from the footer and refuses with a retry hint rather than
  launching in the previous mode.
- `m` on an eligible Flow row marks a GitHub PR that was merged manually:
  Approach verifies the PR is merged with `gh`, records the merge commit and
  timestamp, marks the Merge phase completed, and hides the Flow from active
  lists without launching a Merge phase agent.
- `U` on an eligible Flow row launches an agent in that Flow's worktree with the
  `[flow_prompts].autofix` template, defaulting to `autofix pr #<num>`, where
  `<num>` is the Flow's PR number. Edit that template from the `f2` prompt
  picker. Its gate is
  `m`'s — a non-closed, unmerged Flow with a PR target whose Merge phase is ready
  (or completed at a pending/merged Merge boundary) — plus a non-empty worktree
  path: with no worktree the shortcut is simply unavailable rather than running
  the agent in the repository root. The launch goes through the same lifecycle as
  `g`, so occupancy, the persisted `h` headless preference, tmux mode, and the
  current agent/model/effort all apply, and interactive embedded launches prefill
  the dock for you to send. Their dock tab/chip uses the display identity
  `autofix pr <num>` without `#`, so PR 116 renders, for example,
  `1 codex autofix pr 116 running`; the default agent prompt remains
  `autofix pr #116` unless `[flow_prompts].autofix` overrides it. Headless and
  tmux launches send that rendered prompt on argv; interactive embedded launches
  prefill the dock with it. Headless launches retain the existing Flow identity
  rather than using the interactive autofix label.

  It is deliberately **phase-untracked**: it writes no phase state, marks no
  phase running, and attaches its session to no phase history, so an autofix run
  cannot make the Merge phase someone else's responsibility. Ownership is
  therefore the embedded slot on the embedded route, or — on the tmux route,
  where no slot and no running phase exist — an in-process record of every
  autofix window the Flow has opened, which a second `U`, a phase launch
  (`g`), a phase resume (`r`), and a repair (`R`) all probe before launching,
  because every one of them would land a second agent in the same worktree.
  Every window is retained rather than the newest alone, because the tmux probe
  reports "not live" for a probe it could not run: a press that slipped through
  a transient failure would otherwise erase the still-running window it failed
  to see. That record is not restart-durable, cannot see a *detached*
  embedded terminal, and is not consulted by auto mode, whose poll answers
  without shelling out — so close or dismiss an autofix terminal rather than
  detaching it, and prefer not to leave auto mode advancing a Flow you are
  running autofix on.

  Because occupancy is part of `U`'s footer predicate and not `m`'s, the two are
  not advertised identically: while a launch, a resume, a repair, a Flow terminal,
  or a headless write holds the Flow, `U` withdraws and `m` stays. Refusals name
  the obstacle — `Close or dismiss the existing Flow terminal before running
  autofix`, `A Flow launch is already in flight`, `Flow still has an agent running
  in tmux`. Like `g`, the fresh record decides: a Flow
  that loses its PR, its worktree, or its eligibility before the agent starts is
  refused with `Flow changed; refresh and try again` — at the authoritative read,
  and again at the launch reservation taken immediately before the spawn, which
  is also where the persisted `h` preference is read for the last time. That
  last read is taken under the launch/close lock rather than the Flow store's
  write lock, so it is not ordered against a concurrent `h`; the toggle is
  refused for as long as a `U` attempt holds the Flow, with `A Flow launch is in
  flight; retry the headless change in a moment`, and the launch keeps the mode
  it was admitted with. A live
  session on any non-terminal phase refuses with `Flow already has a running agent
  session`. A stale session on an already-completed phase deliberately does not
  refuse, so one crashed run cannot make `U` permanently dead.
- `C` on an open Flow row opens an input modal demanding a reason, and refuses
  to submit an empty one. Closing records that reason plus a close timestamp,
  renders the row as `closed` with `(closed: <reason>)` after the title, and
  removes it from Active Flows. A closed Flow is terminal: `g`, `R`, `m`, `a`,
  `x`, and `r` are all withdrawn from the footer and refuse, while `h`, `E`,
  `M`, `d`, `c`, `o`, `i`, `p`, `y`, and `enter` still work so the record stays
  inspectable. Phases are left untouched, so a running session keeps running.
  `C` on a closed row reopens it after a `y/n` confirm, restoring exactly the
  launchability it had before the close. Closing is not gated behind destructive
  mode — it destroys nothing. See [flow-phases.md](flow-phases.md) for the
  derived status precedence.
- With destructive mode enabled, `d` deletes only the selected top-level Flow
  record under the Flow artifact store; it does not remove repositories,
  worktrees, branches, checked-out code, linked plans, sessions, transcripts,
  or active embedded terminals. Expanded phase rows cannot be deleted.

Expanded phase rows group child implementation phases directly under
Implementation.

### Repair

`R repair` launches a diagnostic agent only for persisted states that cannot
otherwise proceed: blocked or needs-attention phases, stale or inconsistent
running-session metadata, missing structured PR metadata, or a gated graph
with no launchable phase. Ready work, healthy active sessions, every derived
terminal Flow status, and manual Merge boundaries are intentionally not repair
targets. A matched non-ended provider session counts as healthy active work
even if its phase already says blocked or needs-attention. Any retained Flow
terminal slot — including exited-before-auto-close, failed, terminated,
starting, or prompt-prefill state — blocks repair until it is dismissed or
detached, with `Close, detach, or dismiss the existing Flow
terminal before repairing this Flow`. A repair in flight reserves the Flow from
the key press onward, so a second `R` reports `A repair launch is already
pending for this Flow` and a replayed launch message is a silent no-op; a phase
resume already holding the Flow reports `A phase resume is already pending for
this Flow`. A pending manual or automatic phase launch reports `A phase launch
is already pending for this Flow`; unlike a retained-terminal refusal, this can
occur before any terminal slot exists. Approach fresh-reads the persisted Flow
and rechecks eligibility before allocating the terminal, so a Flow that stopped
being repairable in the meantime reports `Flow is no longer repairable`.

Repair also refuses while the session store holds a live session belonging to
any of the Flow's phase launches, with `Flow phase <phase-id> already has a
running session; if its agent is gone, clear it with approach flow phase reset
--flow-id <flow-id> --phase-id <phase-id>`. The message carries its own remedy,
spelled out so it runs as printed, because unlike the identically-caused resume
refusal repair has no in-app move left — see the trap below. Since the rule
ignores phase status, the remedy is written for the status the phase is actually
in: a phase that has moved off `running` reports `... and is <status>` and, when
it is `blocked` or `needs_attention`, prepends the `approach flow phase restart`
that makes reset legal. Any other status — including `completed` and `skipped`,
which the transition table would let you reopen — says the session metadata has
to be corrected directly rather than naming a command, because no command there
both runs and leaves the phase's result intact. The trap below covers all
three. It names the phase
because the rule is Flow-scoped while the CLI escape is per phase: the phase
reported is the one holding the live session, which is usually not the gated
phase you pressed `R` to unblock. It is wider
than the classifier's own rule above, which sees only sessions a phase has
already mirrored, and its effect is Flow-wide even though
its evidence is per phase: a repair agent may `phase reset`, `phase set`, and
`plan set` anywhere in the record, so it must not run beside a live phase agent
on any phase. A running phase still awaiting session capture with no live
session record anywhere stays repairable — that stale-launch state is the main
reason `R` exists. This is the one repair refusal the footer cannot anticipate:
it is decided against the session store during the authoritative read, so `R`
stays advertised and the press reports the refusal.

Repair is an embedded CLI operation and accepts only effective `codex` or `claude`.
An unset or unsupported effective agent produces guidance instead of launching
an agent or changing Flow state. A phase-scoped
obstruction uses that phase's effective agent/model/effort; a graph-wide
obstruction falls back entirely to globals. The launch also uses that Flow's
persisted `h` headless setting. The reserved pre-launch record is authoritative,
so a phase settings edit committed before reservation is honored.
Interactive repairs prefill their recovery prompt and
focus terminal input; headless repairs submit it and keep list focus.

A repair session carries the Flow ID, linked-plan/worktree metadata, and the
shared artifact root, but deliberately has no Flow phase ID and is not a phase
launch attempt. Hooks may retain it as a Flow-scoped session for
discoverability, but it cannot attach to phase history, create a
`session-mismatch`, or finalize/regress a phase. Its prompt directs the agent
to inspect and mutate the record through structured `approach flow` commands,
never by editing artifact JSON, and never to launch the next phase itself.

### Auto mode

When auto mode is on, Approach runs an always-on, all-repos advance poll that
detects live completed-phase transitions and drains the Flow by launching the
first ready non-merge phase in display order. That choice is then re-checked
against freshly read Flow state, but not re-made: the phase the poll picked
launches as long as it is still launchable, even if an earlier phase became
ready in the meantime. Auto-launched CLI phases are
always headless and do not change the current view or focus. Approach launches
at most one phase per Flow at a time: if any phase is running or any
Flow-scoped embedded terminal is still open or auto-closing, the drain waits.
It also waits while a manual phase resume or a repair on that Flow is still in
flight — both hold a launch attempt from their key press onward — and while a
session recorded against the phase it would launch has not ended. Each of those
defers the launch silently and it resumes on a later poll.

That last one is what `x` recovers. A session is treated as live until it is
recorded as ended, and only an orderly exit records that, so an agent whose
session is never finalized — Approach killed, the machine lost — leaves the
phase looking permanently busy. Auto mode then waits forever, repair reports no
obstruction because the phase is still launchable, and reset applies only to
`running` phases. Launching the phase manually with `g` says so, and names the
phase because `g` acts on the Flow while release acts on the selection: `Flow
phase implementation has an unfinished session; select it and x releases it`.

`x` on that phase releases the session. It reads the phase's launches from both
the session store and the Flow record, and asks before acting — *Release 1
unverified session (…1a2b3c4d)? Recorded as ended.* — because Approach cannot
prove those agents are gone: outside the embedded terminal and tmux there is
nothing to probe. Confirming records each launch as ended, exactly as a clean
exit would have, and the phase becomes launchable again. When that Flow's auto
mode drain is armed in the running process, the prompt says so instead —
*AutoMode may launch a phase.* — because the next poll starts an agent within a
second. It stays that vague on purpose: the drain picks its own candidate when
it runs, which need not be the phase whose session you released.

Release refuses whole while an agent is provably live on the phase — a running
embedded terminal, or a live tmux window for any of its launches — rather than
releasing the stale launch beside it. That case is reachable after `r`, which
starts a second agent on the same phase and persists a second launch ID:
dismiss the embedded terminal, or attach with `T` and exit the agent in its tmux
window, then press `x`.

Resuming the stalled session with `r` still works and still gets you back into
the work — resume deliberately does not count the session it is reattaching to
as occupancy — but it is not a fix. Whether it clears the stall depends on the
provider: one that reuses the session ID rebinds the old record to the new
launch and a clean exit ends it, while one that mints a fresh ID leaves the
crashed record live forever.

The footer advertises `x release session` only for a `ready` phase, where a live
session contradicts the phase's own status and is worth pointing at. Every other
status is one a live session is unsurprising on: `running` is where a working
agent belongs, and `completed`, `blocked`, `needs_attention`, and `skipped` are
all set by the agent itself from inside its own session — the gap between
`approach flow phase complete` and the agent exiting is ordinary. Those phases
are recovered the same way — press `x` and it probes — they are just not
advertised, because a standing hint beside a healthy agent would read as an
invitation.

There is a matching trap one step earlier, before a session is ever attached to
its phase. A session record is written before the phase attach, and the attach
error is discarded, so nothing finalizes that record if the agent dies or the
attach fails in between. The phase's newest launch is then backed by a
permanently live-looking store record. Repair cannot tell that from an agent
that is genuinely still starting, so every `R` press refuses until that record
ends. The refusal names `approach flow phase reset` for exactly this reason:
the user has no way to clear it from the TUI, so the message has to point out
of it. `R` keeps being
advertised throughout: the footer answers from the cached Flow record only, and
this rule is evaluated against the session store during the launch's
authoritative read, which is one asynchronous hop past the footer. Advertised
and refused is the honest reading here — the footer cannot see the blocker.

The route out is the CLI, and which command depends on the phase's status — so
the refusal prints the one that matches. When the phase is still `running`,
`approach flow phase reset` clears the stale launch on its own. When the agent
moved the phase off `running` before it died — to `blocked` or
`needs_attention`, say — reset alone refuses with `flow phase reset requires
running recoverable phase`, so the refusal prints two steps instead: `approach
flow phase restart`, which returns the phase to `running` and supplies the note
that a bare `phase set --status running` would demand, then `approach flow phase
reset`. If the
phase also carries session metadata that disagrees with its launch IDs, reset
refuses again with `flow phase reset requires attached sessions to match phase
launch ids`, and the record has to be corrected before either route works.

Reset clears only the phase's newest launch, while the occupancy rule matches a
live session against every launch the phase has recorded. A phase that
accumulated more than one launch, where the stranded live record belongs to an
older one, therefore stays blocked after a reset that reported success — and `g`
refuses on the same evidence, so there is no in-app move left either. Correcting
that phase's session metadata directly is the only way out. That reset leaves
the phase `pending`, which has no non-destructive route back to `running`, so
the refusal stops naming a command there and says the metadata has to be
corrected directly. The refusal does not try to predict that residue for the
`running` case: reset's own preconditions live in the store, and restating them
in a status message would put a second copy of them one layer away.

The occupancy rule deliberately does not filter by phase status, because a live
session on a phase that has already been marked blocked is still an agent that
may be working; that is the same judgment the classifier above makes.

A 3 s status message announces auto-launches, `needs_attention`, and
merge-ready transitions. Any status from elsewhere in the app blocks all of
them. Among themselves they are ranked rather than ordered by arrival, because
auto-launch and launch-failure messages are emitted after the Flow is re-read
rather than when the poll decides and would otherwise displace a transition
raised by the same poll, possibly on a different Flow. A `needs_attention` or
merge-ready message therefore outranks both and is not replaced while it is on
screen; two launch messages replace each other, so the most recent one shows.
One consequence is deliberate: a launch that happens within 3 s of a transition
is never announced, because the announcement is not retried. The launch still
runs and the phase still shows as running. A launch *failure* is retried, so it
appears once the transition clears.

Skipped, blocked, needs-attention, failed-launch, or missing-PR-metadata states
do not auto-launch. Automation stops before Merge: if Autoreview completes and Merge
becomes ready, Approach keeps auto mode on and requires the manual Merge launch.
A repair terminal that exits cleanly and is then auto-closed or dismissed arms
a one-shot, generation-fenced auto drain. The first successful all-Flows poll
that began after that exit may launch the newly ready non-merge successor,
including after metadata-only repair or a running-to-ready reset. Stale,
pre-exit, and failed polls retain the handoff marker; auto mode off, failed or
terminated repair, detach, and Merge-only readiness clear or bypass it without
launching. While a repair terminal is retained, ordinary completion edges for
that Flow are held. A non-clean removal keeps suppressing repair-caused edges
until a later state first exposes work that auto mode could otherwise launch,
so failure, termination, or a detached agent's delayed mutation cannot leak a
successor launch. A later clean repair retry replaces the earlier suppressing
outcome and receives its normal one-shot handoff.

### Headless mode, model, and effort

Each Flow persists its own headless preference, which defaults on. Selected CLI
`codex` and `claude` phase launches run in the shared runtime-only embedded
terminal dock. Press `h` on a Flow or one of its expanded phase rows to choose
that Flow's manual CLI command mode: headless runs `codex exec` or
`claude --print`, while headless off runs interactive `codex` or `claude` in
the same dock. Headless-off launches prefill the phase prompt without
submitting it, then focus the terminal in input mode so
you can review or edit it before pressing enter. Headless launches keep focus
on the Flow list. The New Flow form's default-on Headless checkbox seeds the
persisted preference whether Plan Now is on or off; an immediate Plan launch
and a later launch of a parked Flow both use that stored value. Records created
before the field existed read as headless-on without being rewritten solely by
a read. Manual phase and repair launches use the target Flow's value. Auto-mode
CLI launches are always headless, independent of it; non-Flow and phase-resume
behavior is unchanged. Flow-associated saved-session resumes ignore the
preference and are always interactive.

The toggle and launches fence each other in both directions. A launch started
while a headless write is still settling is refused with `Applying headless mode
change; retry the launch in a moment`. In the other direction the `U` autofix
agent and an `R` repair block the toggle — they are the two headless-aware
phase-untracked kinds, and each resolves the preference in its prepare stage
from a record taken under the launch/close lock, which is not ordered against
the write — so
pressing `h` while either attempt holds the Flow is refused with `A Flow launch
is in flight; retry the headless change in a moment`. Tracked phase launches are
not fenced: they read headless from the record their own phase write returns,
which is ordered against the toggle. Generic `s` ignores the persisted headless
preference and always opens an interactive dock terminal, so it does not need
this fence.

On a Flow row, `A`, `M`, and `E` continue to edit the global agent preferences.
On a selected expanded phase row in Flows or Active Flows, the same keys show
that phase's effective values and edit only its persisted stamp. The phase
agent picker includes `inherit global settings`, which clears the whole stamp;
choosing a provider writes an agent-only stamp so model and effort follow that
provider's globals. Phase model and effort pickers include `inherit global`,
which clears only that field, separately from the literal `default` provider
choice. If the raw phase agent is empty, choosing an explicit model or effort
also stamps the effective provider but does not materialize the unrelated
fallback field. Repository focus and non-Flow panes keep the global controls.
The equivalent whole-stamp CLI is `approach flow phase agent set`; pass
`--clear` to remove it.

Codex CLI
launches use `--model <model>` and `--config model_reasoning_effort=<effort>`;
Claude launches use `--model <model>` and `--effort <effort>`. Session resumes
do not receive model or effort flags.

Embedded headless output is readable terminal text, not raw JSON events:
`codex exec` streams its progress directly, while `claude --print` runs with
`--output-format stream-json --include-partial-messages` and Approach translates
those events into readable lines (thinking, tool calls and results, the final
answer streamed token-by-token, and a closing summary).

### Flow terminals

Flow terminals share the persistent dock, global terminal-tab numbering, focus
cycle, and command set described in [Embedded Terminals](#embedded-terminals). Selecting
a Flow or phase still synchronizes the active dock terminal to its attached
Flow terminal when one exists. Flow scope remains lifecycle metadata for
launch tracking, auto-mode gating, and recovery; it no longer creates a
separate terminal surface or command path. Repair tabs use the explicit
`repair` identity and retain only Flow-level scope, never a phase attachment.

### Recovery labels

Flow rows surface recoverable partial states so they are not confused with
ordinary empty or pending work:

| Label | Meaning |
|-------|---------|
| `recover-worktree` | Saved Flow with no branch/worktree metadata; a phase launch creates the missing worktree, and is refused with a message when it cannot |
| `await-session` | Running phase with a recorded launch but no attached session yet |
| `session-mismatch` | Attached session whose launch ID does not match the phase's launch attempts |
| `ended-session` | Running phase whose latest attached session has ended |
| `missing-session-id` | Attached session that lacks a provider session ID |
| `missing-pr` | Pending Autoreview phase whose PR Creation predecessor completed without structured PR metadata |

When an expanded phase row shows `await-session` or `ended-session`, and no
running or starting embedded Flow terminal is attached to that same Flow
phase, the selected phase row exposes `x reset ready`. Confirming the prompt
recovers the abandoned launch attempt and derives the phase back to `ready`;
`approach flow phase reset` performs the same recovery from the CLI. The reset
semantics, rejection rules, and phase gating are documented in
`docs/flow-phases.md`.

### Phase progression

The TUI can create a new Flow and record a launch for the next launchable
phase; agents perform all other phase progression through the `approach flow`
CLI. The canonical agent instructions are the `approach-flow` and
`approach-flow-create` skills (`agent-skills/approach-flow/SKILL.md`,
`agent-skills/approach-flow-create/SKILL.md`); phase transitions, derived
readiness, gating, and merge requirements are documented in
`docs/flow-phases.md`.

## Active Flows View (`ctrl+a`)

Browse active Flow records across all repos. Press `ctrl+a` from any normal
TUI view to open it across the combined content height, and press `ctrl+a`
again to restore the exact top mode, bottom mode, remembered pane, and focus;
number keys and arrows do not leave Active Flows. This view hides merged and
closed Flow records; moving focus to the left repo pane temporarily filters the
visible active rows to the selected repo, and returning focus to the middle pane
restores the global list. Normal Flow actions — phase launches, repairs,
attached-session resumes, auto-mode toggles, `i` issue and `p` PR opening,
`c`/`y` copies, and embedded Flow terminals in the shared dock — work from the
visible active Flow rows and their expanded phase rows. `C` closes a Flow from
here too, which removes its row; reopening is reachable only from the repo's
flows pane, since a closed Flow has no Active Flows row to select.

## PR Babysitter View (`ctrl+p`)

Press `ctrl+p` from any normal view to watch GitHub PRs that are waiting at a
Flow's Merge boundary. The takeover spans the combined content height beside
repos and above the shared terminal dock. Press `ctrl+p` again to restore the
exact original top mode, bottom mode, remembered content pane, and focus.
Number keys and horizontal arrows do not enter or leave it.

`ctrl+a` while PR Babysitter is open switches directly to Active Flows;
`ctrl+p` from Active Flows switches directly to PR Babysitter. A cross-switch
keeps the current repo/content focus and the original return snapshot. Closing
the destination takeover with its own key still restores the view that was
active before the first takeover opened.

Each row shows repository, terminal-safe Flow title, linked Bead ID (blank when
the Flow has no Bead link), live GitHub mergeability, and live checks status.
Mergeability is `mergeable`, `conflicting`, or `unknown`; checks are `passing`,
`failing`, `pending`, or `unknown`. Expanding a row with `enter` renders the
ordinary Flow phase details beneath those dashboard columns.

A Flow qualifies only when it is non-terminal, has a complete GitHub PR target,
has exactly one top-level semantic Merge phase, and has no unresolved recovered
graph edges. A ready or completed Merge phase must still have satisfied
predecessors and pending merge metadata. A blocked Merge phase remains visible
with pending or blocked merge metadata even if an upstream predecessor was
later reopened. Merged, closed, abandoned, duplicate-Merge, child-Merge, and
already-recorded-merged records are excluded.

GitHub status is queried with `gh` when the view opens, every 30 seconds after
the newest visible refresh settles, and immediately when `f5` supersedes the
current refresh. At most four PR queries run concurrently and each has a
10-second timeout. Leaving or cross-switching the view cancels in-flight
processes and hidden views generate no `gh` traffic. A failed Flow-list refresh
keeps cached rows and statuses with a cached-data warning; an individual `gh`
failure replaces that row's two live columns with `unknown` and does not break
the dashboard.

Moving focus to repos filters rows to the selected repository; returning to
content restores the global list and preserves selection by Flow ID when it is
still present. The shared Flow handlers provide phase expansion and actions such
as `p`, `c`, `C`, and `i`. `m` and `U` retain their ordinary manual-merge gate:
a blocked Merge row can remain useful to babysit without gaining either action.

As with `ctrl+a`, a tmux configuration that uses `ctrl+p` as its prefix consumes
the key before Approach. Use that tmux configuration's prefix-passthrough
sequence to send `ctrl+p` through to the TUI.
