# TUI Guide

Complete reference for Approach's terminal UI: panes, key bindings, and
per-view behavior. `README.md` has the short version. Related references:
config in `docs/config.md`, Flow phase semantics in `docs/flow-phases.md`,
session hooks and storage in `docs/agent-sessions.md`.

## Layout and Focus

The UI has a repos pane on the left, a stacked content column in the middle,
and shortcuts on the right. Git/Beads is the top content pane;
Sessions/Plans/Flows is the bottom pane. Approach starts with repos focused,
Beads/Open stored above Flows, and the top pane remembered as the first content
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
visible. Active Flows is a separate takeover that always spans the combined
content height and restores both stored panes exactly when closed.

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
| `A` | Choose and persist the coding agent from a picker (`codex`, `codex-app`, or `claude`) |
| `D` | Toggle destructive mode |
| `f` | Fetch all currently visible repos with `--prune` |
| `n` | Create a new local repo under the scan root, optionally creating a GitHub repo and wiring `origin` |
| `enter` | Collapse the repos pane and focus the top content pane |
| `tab` | Focus the top content pane without collapsing the repos pane |
| `f2` | Edit prompt templates |
| `q`/`esc` | Quit |

**Content panes**

| Key | Action |
|-----|--------|
| `↑`/`k` | Move selection up |
| `↓`/`j` | Move selection down |
| `/` | Fuzzy filter the current item list, including the active Beads subview |
| Top `1` / `2` | Switch the top pane to Git / Beads at its last-used subview; Beads defaults to Open before first use |
| Bottom `1` / `2` / `3` | Switch the bottom pane to Sessions / Plans / Flows |
| `ctrl+a` | Toggle Active Flows; pressing it again from Active Flows returns to the previous view. In tmux sessions that use `ctrl+a` as the prefix, send the prefix passthrough first. |
| `w`/`b`/`s`/`h`/`r` | Inside the Git view, switch directly to the worktrees / branches / stashes / history / reflog subview |
| `r`/`b`/`o`/`i`/`c` | Inside Beads, switch directly to the ready / blocked / open / in-progress / closed subview; the same letters keep their existing meanings outside Beads |
| `←`/`→` | Wrap between Git and Beads in the top pane, or Sessions, Plans, and Flows in the bottom pane; grouped entries use their remembered subview. Active Flows is not in either cycle. |
| `h` | Switch to the history subview inside the Git view; toggle Flow headless/interactive command mode in flows view |
| `M` | Choose and persist model for the selected CLI agent in flows view |
| `E` | Choose and persist reasoning effort for the selected CLI agent in flows view |
| `enter` | Page diff in `less` (dirty worktree, dirty branch, stash, commit, or reflog entry), page a selected bead's detail, resume an inline worktree session, page a session transcript, or expand/collapse plan or Flow phases |
| `g` | Launch the next launchable phase for the selected Flow in flows view |
| `R` | Launch an embedded repair agent for a genuinely stalled selected Flow in flows or Active Flows view |
| `n` | Create a new worktree in worktrees view, a new branch in branches view, or a new Flow in flows view |
| `P` | Create a review worktree from a GitHub PR number or URL |
| `N` | Create a new worktree and launch the selected coding agent |
| `m` | Move or rename a linked worktree (worktrees view), or mark the selected Flow's GitHub PR as already merged after verifying it in GitHub (flows and active flows views) |
| `A` | Choose and persist the coding agent from a picker (`codex`, `codex-app`, or `claude`) |
| `a` | Launch the selected coding agent in the selected worktree, launch the selected plan or plan phase, or toggle auto mode for the selected Flow (flows and active flows views) |
| `d` | Delete worktree/branch, drop stash, or delete Flow data — requires destructive mode |
| `p` | Prune stale worktree — requires destructive mode (worktrees view), or open the linked PR (flows and active flows views, when PR metadata exists) |
| `u` | Unlock a locked worktree (worktrees view) |
| `f` | Fetch with `--prune` (worktrees and branches views), or create a parked Flow with its worktree for the selected Bead in a settled Ready subview |
| `F` | Pull with `--ff-only` (worktrees, and branches with a checked-out worktree) |
| `t` | Open or attach to a tmux/Zellij session for the worktree |
| `c` | Open VSCode at worktree path outside Flow surfaces, or copy the selected Flow ID in flows and active flows views |
| `x` | Show/hide sessions for the selected worktree (worktrees view), expand/collapse plan phase rows, or reset a selected recoverable Flow phase after confirmation |
| `y` | Copy hash to clipboard (history/reflog view), selected agent session ID (sessions view), plan Markdown path (plans view), or selected Flow worktree path (flows view) |
| `r` | Resume selected agent session (sessions view; CLI agents embed in-pane) or selected attached Flow phase session (flows view) |
| `s` | Page selected agent session summary (sessions view) |
| `o` | Page selected session transcript (sessions view), selected plan Markdown (plans view), or linked plan body (flows view) |
| `e` | Edit selected plan Markdown (plans view) |
| `i` | Alias for plan implementation launch, or open the linked GitHub issue (flows and active flows views, when issue metadata exists) |
| `D` | Toggle destructive mode |
| `ctrl+r` | Restore and focus the full repos pane (outside search or embedded-terminal input focus) |
| `f5` | Rescan repositories and refetch both stored panes; while Active Flows is open, refresh it independently too |
| `ctrl+t` | Hide or show the shared embedded terminal dock (outside search and terminal input); when input is focused, use `ctrl+] t` |
| `tab` | Cycle focus forward through repos, top, bottom, and an eligible terminal; collapsed repos are skipped |
| `bksp` | Cycle focus in reverse (outside search or embedded-terminal input focus) |
| `f2` | Edit prompt templates |
| `q`/`esc` | Close a prompt/dialog or quit |

## View Switching and the Header

The top header shows local `1` Git and `2` Beads, with `^a` Active Flows pinned
to the right. The bottom header independently shows local `1` Sessions, `2`
Plans, and `3` Flows. Numbers are silent no-ops from repos, Active Flows, the
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
last-used subview (Open before first use), and each subview keeps its own
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

Press `A` to choose `codex`, `codex-app`, or `claude` from a picker; Approach
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

How sessions are captured, where they are stored, and how resume picks its
working directory are covered in `docs/agent-sessions.md`.

## Embedded Terminals

Resuming a CLI `codex` or `claude` session, launching a CLI Flow phase, or
repairing a stalled Flow opens
a runtime-only terminal in one shared full-width dock — a top-level pane below
the repo, content, and shortcut panes, directly above the status bar.
The dock persists while switching among Git, sessions, plans, flows, and
Active Flows. Its terminal numbers and active selection are global, so a
session resume and a Flow launch appear in the same numbered header. The
current list remains usable above the dock; in particular, the saved-session
table is never replaced by the terminal surface.

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
Beads group at its last-used subview, defaulting to Open before first use.
Beads queries and detail reads are read-only, and no action in this group
mutates tracker state. Pressing `2` while already in any Beads subview is a
no-op. Press `r` for
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

In Ready only, `f` is available when the content pane is focused, the query is
settled and available, a filtered visible Bead with a non-empty ID is selected,
and no Ready Flow creation is already in flight. It asynchronously creates
exactly one Approach Flow in the selected repo. The record title is
`<trimmed bead ID>: <trimmed bead title>` and its instructions are
``Use Bead <id> as the durable source of requirements. Read it with `bd show <id>` before planning or implementation.`` The configured Flow preset seeds the
phase graph and normal Flow creation defaults apply. The shortcut prepares the
Flow exactly like an `n` form submission with Plan Now off: it creates the
`flow/<slug>` branch and worktree from the repository's current HEAD, records
the worktree, branch, and commit start metadata, and runs the repository's
bootstrap hook. It does not link a plan or issue, start a Flow phase, launch an
agent, or invoke `bd`, so the selected Bead and all other tracker state remain
untouched. The shortcut is hidden in every context where that exact action
cannot run; duplicate keypresses are ignored until the current creation request
finishes, and any repo change — cursor move or rescan — releases the shortcut
and discards the pending result.

The result is the same parked Flow a successful `n` form submission produces,
so `g` on its first phase launches the agent inside the Flow's isolated
worktree. If worktree creation or the bootstrap hook fails, the persisted Flow
record keeps its launchable phases blocked with the failure noted, the Flows
pane renders the worktree-less record with the `missing-worktree` branch label
and a `recover-worktree` phase state, and the error is reported in the status
line.

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
launched later from the Flow row.

On a Flow row or an expanded phase row:

- `enter` expands or collapses the phase detail rows.
- `o` pages the linked plan body in `less -R`; Approach shows a status message
  when the selected Flow has no linked plan.
- `g` launches the first launchable phase in the selected Flow's canonical
  phase order. This uses the selected Flow, so a highlighted pending phase row
  can still launch an earlier ready sibling; nothing is persisted when no
  phase is launchable.
- `R` repairs a genuinely stalled nonterminal Flow from either its top-level
  row or an expanded phase row. The shortcut is shown only when no phase can
  be launched manually, no healthy phase session is running, no Flow terminal
  slot is occupied, and the Flow is not completed, merged, abandoned, or at a
  ready/manual Merge boundary.
- `i` opens the linked GitHub issue and `p` opens the linked PR in the
  browser, when that metadata exists.
- `c` copies the selected Flow ID; `y` copies the selected Flow worktree path.
- `r` on an expanded phase row with an attached provider session resumes that
  session. CLI resumes are recorded as a fresh Flow phase launch attempt;
  `codex-app` resumes navigate to the existing app thread without extra launch
  tracking.
- `a` toggles per-Flow auto mode, which is on by default for new Flows and
  persisted on that Flow record. Flows created before this field existed
  remain manual until toggled on.
- `m` on an eligible Flow row marks a GitHub PR that was merged manually:
  Approach verifies the PR is merged with `gh`, records the merge commit and
  timestamp, marks the Merge phase completed, and hides the Flow from active
  lists without launching a Merge phase agent.
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
even if its phase already says blocked or needs-attention. Any retained Flow terminal slot — including exited-before-auto-close,
failed, terminated, starting, or prompt-prefill state — blocks repair until it
is dismissed or detached. A pending repair launch also reserves the Flow, so
repeated `R` input or a replayed/stale launch message cannot start a second
repair agent; Approach fresh-reads the persisted Flow and rechecks eligibility
before allocating the terminal.

Repair is an embedded CLI operation and accepts only `codex` or `claude`.
`codex-app`, an unset agent, or another configured command produces guidance
instead of opening an external app or changing Flow state. The launch reuses
the selected provider's current model and reasoning effort plus the manual
`h` headless setting. Interactive repairs prefill their recovery prompt and
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
first ready non-merge phase in display order. Auto-launched CLI phases are
always headless and do not change the current view or focus. Approach launches
at most one phase per Flow at a time: if any phase is running or any
Flow-scoped embedded terminal is still open or auto-closing, the drain waits.
A 3 s status message announces auto-launches, `needs_attention`, and
merge-ready transitions unless another status message is active. Skipped,
blocked, needs-attention, failed-launch, or missing-PR-metadata states do not
auto-launch. Automation stops before Merge: if Autoreview completes and Merge
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

Flow headless mode is on by default: selected CLI `codex` and `claude` phase
launches run in the shared runtime-only embedded terminal dock. Press
`h` to choose the CLI command mode: headless runs `codex exec` or
`claude --print`, while headless off runs interactive `codex` or `claude` in
the same dock. Headless-off launches prefill the phase prompt without
submitting it, then focus the terminal in input mode so
you can review or edit it before pressing enter. Headless launches keep focus
on the Flow list. Creating a new Flow has its own default-on Headless checkbox
for the initial Plan launch; that checkbox is ignored when Plan Now is off and
does not change the selected-phase `h` setting. The `h` toggle applies only to
manual phase launches; auto-mode CLI launches are always headless.

Press `M` to choose the selected CLI agent's model and `E` to choose its
reasoning effort; the shortcut pane shows the current values. Codex CLI
launches use `--model <model>` and `--config model_reasoning_effort=<effort>`;
Claude launches use `--model <model>` and `--effort <effort>`. Session resumes
do not receive model or effort flags. `codex-app` always uses the external
deep-link route and keeps app-side/default model and reasoning.

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
| `recover-worktree` | Saved Flow with no branch/worktree metadata |
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
number keys and arrows do not leave Active Flows. This view hides merged Flow
records; moving focus to the left repo pane temporarily filters the visible
active rows to the selected repo, and returning focus to the middle pane
restores the global list. Normal Flow actions — phase launches, repairs,
attached-session resumes, auto-mode toggles, `i` issue and `p` PR opening,
`c`/`y` copies, and embedded Flow terminals in the shared dock — work from the
visible active Flow rows and their expanded phase rows.
