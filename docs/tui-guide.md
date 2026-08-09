# TUI Guide

Complete reference for Approach's terminal UI: panes, key bindings, and
per-view behavior. `README.md` has the short version. Related references:
config in `docs/config.md`, Flow phase semantics in `docs/flow-phases.md`,
session hooks and storage in `docs/agent-sessions.md`.

## Layout and Focus

The UI has two panes: repos on the left, content on the right. Press `enter`
or `tab` on a selected repo to focus the content pane; from the content pane,
`tab` or `bksp` returns focus to the repo pane. When a Flow embedded terminal
is open, `tab` cycles through the repo pane, Flow list, and terminal. The
active pane is highlighted with a blue border.

Press `f2` from normal TUI views to open the prompt-template editor for the
`[agent].plan_prompt` and `[flow_prompts]` templates; Flow terminal input
focus passes F2 through to the embedded agent.

Empty panes explain why they are empty: no data for the selected repo, no
fuzzy filter matches, or a load failure with details in the status bar. Beads
uses subview-specific quiet empty/loading messages and the shared
`beads not configured` state described below.

**Destructive mode:** The app starts in read-only mode — deletion keys are
disabled. Press `D` (Shift+D) to toggle destructive mode on/off. When active,
the right pane border turns red and delete/drop hints appear in red as a
visual warning.

**Fuzzy filter:** Press `/` in the active pane to type-ahead filter repos or
right-pane items. `enter` keeps the filter, `esc` clears it, `backspace` edits
it. Each filterable right-pane view keeps its own filter: filtering worktrees
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
| `V` | Choose and persist the startup default view from a grouped picker (`Git — Worktrees` … `Active Flows`) |
| `D` | Toggle destructive mode |
| `f` | Fetch all currently visible repos with `--prune` |
| `n` | Create a new local repo under the scan root, optionally creating a GitHub repo and wiring `origin` |
| `enter`/`tab` | Switch focus to right pane |
| `f2` | Edit prompt templates |
| `q`/`esc` | Quit |

**Right pane (content)**

| Key | Action |
|-----|--------|
| `↑`/`k` | Move selection up |
| `↓`/`j` | Move selection down |
| `/` | Fuzzy filter the current item list, including the active Beads subview |
| `1`/`2`/`3`/`4`/`5` | Switch to the Git view / sessions / plans / flows / Beads outside Active Flows (`6`–`9` are unbound); `1` and `5` return to their group's last-used subview and are no-ops while already inside that group; Beads defaults to Open before first use |
| `ctrl+a` | Toggle Active Flows; pressing it again from Active Flows returns to the previous view. In tmux sessions that use `ctrl+a` as the prefix, send the prefix passthrough first. |
| `w`/`b`/`s`/`h`/`r` | Inside the Git view, switch directly to the worktrees / branches / stashes / history / reflog subview |
| `r`/`b`/`o`/`i`/`c` | Inside Beads, switch directly to the ready / blocked / open / in-progress / closed subview; the same letters keep their existing meanings outside Beads |
| `←`/`→` | Cycle subviews with wrap inside either Git or Beads (arrows never leave a grouped view); elsewhere step through Git, sessions, plans, flows, and Beads, entering either group at its last-used subview. Active Flows is not in the arrow cycle. |
| `l` | Alias for `→` in flows view, entering the last-used Beads subview; unbound elsewhere |
| `h` | Switch to the history subview inside the Git view; toggle Flow headless/interactive command mode in flows view |
| `M` | Choose and persist model for the selected CLI agent in flows view |
| `E` | Choose and persist reasoning effort for the selected CLI agent in flows view |
| `enter` | Page diff in `less` (dirty worktree, dirty branch, stash, commit, or reflog entry), resume an inline worktree session, page a session transcript, or expand/collapse plan or Flow phases |
| `g` | Launch the next launchable phase for the selected Flow in flows view |
| `n` | Create a new worktree in worktrees view, a new branch in branches view, or a new Flow in flows view |
| `P` | Create a review worktree from a GitHub PR number or URL |
| `N` | Create a new worktree and launch the selected coding agent |
| `m` | Move or rename a linked worktree (worktrees view), or mark the selected Flow's GitHub PR as already merged after verifying it in GitHub (flows and active flows views) |
| `A` | Choose and persist the coding agent from a picker (`codex`, `codex-app`, or `claude`) |
| `V` | Choose and persist the startup default view from a grouped picker (`Git — Worktrees` … `Active Flows`) |
| `a` | Launch the selected coding agent in the selected worktree, launch the selected plan or plan phase, or toggle auto mode for the selected Flow (flows and active flows views) |
| `d` | Delete worktree/branch, drop stash, or delete Flow data — requires destructive mode |
| `p` | Prune stale worktree — requires destructive mode (worktrees view), or open the linked PR (flows and active flows views, when PR metadata exists) |
| `u` | Unlock a locked worktree (worktrees view) |
| `f` | Fetch with `--prune` (worktrees and branches views) |
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
| `f5` | Rescan repositories and refetch the current view, including the active Beads subview |
| `tab` | Cycle pane focus forward; with a Flow terminal open, cycles repo pane → Flow list → terminal |
| `bksp` | Switch focus to left pane |
| `f2` | Edit prompt templates |
| `q`/`esc` | Close a prompt/dialog or quit |

## View Switching and the Header

The right pane header shows the top-level views: `1` git, `2` sessions, `3`
plans, `4` flows, and `5` beads on the left, with `^a` active flows pinned to
the right.
While the Git view is active a second header row lists its subviews with their
direct letter keys (`w` worktrees, `b` branches, `s` stashes, `h` history, `r`
reflog); the active entries are bracketed. Entering the Git view lands on the
last-used subview (worktrees on first entry), and each subview keeps its own
cursor position and filter across switches. Press `V` to choose which view
Approach opens on future launches; leaving it unset keeps the built-in startup
default of Flows.

While Beads is active, its second header row lists `r` ready, `b` blocked, `o`
open, `i` in-progress, and `c` closed. The active top-level Beads entry and
active subview are bracketed. This extra row comes out of the list viewport,
so it does not increase the pane's outer height. Entering Beads lands on the
last-used subview (Open before first use), arrows wrap within the five Beads
subviews, and each subview keeps its own filter, cursor, and scroll position.

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

## Git View: Worktrees Subview (`1`, then `w`)

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

## Sessions View (`2`)

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

Resuming a CLI `codex` or `claude` session from the full sessions view opens a
runtime-only embedded terminal in the sessions pane. While embedded terminals
exist, the saved-session table is hidden and the pane shows a compact numbered
terminal header plus the active terminal screen. While the session terminal
right pane is focused, all keys except `tab` go directly to the active PTY
(including agent shortcuts like `ctrl+g`); after tabbing to the left pane,
repo pane keys operate normally.

Press `ctrl+]` for Approach commands:

| Command | Action |
|---------|--------|
| `ctrl+] 1`–`9` | Switch terminals |
| `ctrl+] l` | Open a saved-session picker |
| `ctrl+] d` | Detach a tmux-backed terminal and open a new external terminal attached to that tmux session |
| `ctrl+] x` | Dismiss an exited terminal or confirm termination of a running one |
| `ctrl+] q` / `ctrl+] esc` | Quit with cleanup |
| `ctrl+] ctrl+]` | Send a literal `ctrl+]` to the agent |

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

## Plans View (`3`)

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

## Beads View (`5`)

With the content pane focused, press `5` to enter the selected repository's
read-only Beads group at its last-used subview, defaulting to Open before first
use. Pressing `5` while already in any Beads subview is a no-op. Press `r` for
Ready, `b` for Blocked, `o` for Open, `i` for In-Progress, or `c` for Closed;
pressing the already-active letter is also a no-op. `←`/`→` step and wrap
Ready ↔ Blocked ↔ Open ↔ In-Progress ↔ Closed without leaving the group. From
Flows, `→` (or `l`) enters Beads at the remembered subview as the fifth
top-level arrow stop. The five modes keep independent rows, filters,
cursor/scroll positions, availability, loading state, and request tokens.

The query sources and ordering are:

- Ready: `bd ready --json --limit 0 --readonly`, sorted by priority then natural ID.
- Blocked: `bd list -s blocked --json --limit 0 --readonly`, sorted by priority then natural ID.
- Open: `bd list -s open --json --limit 0 --readonly`, sorted by priority then natural ID.
- In-Progress: `bd list -s in_progress --json --limit 0 --readonly`, sorted by priority then natural ID.
- Closed: `bd list -s closed --json --limit 100 --sort closed --readonly`, selecting the newest 100 before the result is parsed and sorted by descending `closed_at`, then natural ID. The full total comes from `bd stats --json --no-activity --readonly` at `summary.closed_issues` (or the v1 `data.summary.closed_issues` envelope).

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
use the corresponding `loading ... beads` message. A missing `bd` binary, a
repository without a Beads database, or any command or JSON parsing failure
shows exactly `beads not configured`; configured-versus-error classification
remains deferred.

`/` filters only the active Beads pane by ID, title, and assignee. Subview
switches and `f5` retain that pane's same-repo rows, query, cursor, and scroll
internally while the loading UI hides old rows. A loading pane shows only its
`loading` message and ignores cursor keys, so no selection moves behind it. An
accepted replacement reapplies the filter and clamps selection and scroll for
shorter, empty, or zero-match results. An unavailable result retains the query
but clears that pane's rows and selection. Moving to another repo invalidates
every Beads request, clears all old-repo rows and cursor/scroll positions,
retains each subview's query, and starts only the active subview pending for the
new repo; retention is same-repo only, so this holds even when the repo changes
while Active Flows is open.
Per-mode request tokens reject results for an old repo, an older refresh, or a
subview that is no longer active. Every query is read-only, and `bd -C` plus the
selected process directory are owned by the query runner.

`enter` still has no Beads detail pager. Configured-versus-error classification,
pager, and `default_view` 10–14 additions remain deferred; keys `6`–`9` and the
existing frozen 1–9 startup vocabulary are unchanged.

## Flows View (`4`)

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

### Headless mode, model, and effort

Flow headless mode is on by default: selected CLI `codex` and `claude` phase
launches run in a runtime-only embedded terminal inside the flows pane. Press
`h` to choose the CLI command mode: headless runs `codex exec` or
`claude --print`, while headless off runs interactive `codex` or `claude` in
the same embedded Flow terminal. Headless-off launches prefill the phase
prompt without submitting it, then focus the Flow terminal in input mode so
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

While a Flow terminal is open, the Flow list uses a smaller top panel and the
terminal uses a bottom panel; `tab` cycles focus through the repo pane, Flow
list, and Flow terminal. Manually tabbing into Flow terminal focus starts in
Approach command mode: `left`/`right` cycle Flow terminals, `1`–`9` switches by
number, `x` closes, `d` detaches to tmux when available and opens the detached
session in an external terminal, `q`/`esc` quits, unknown ordinary keys do not
pass through to the PTY, `ctrl+]` sends a literal `ctrl+]`, and `i` enters
terminal input mode. In input mode, keys pass through to the PTY (including
agent shortcuts like `ctrl+g`) except `tab`, which cycles pane focus, and
`ctrl+]`, which returns to command mode.

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
TUI view to open it, and press `ctrl+a` again to return to the previous view;
number keys and arrows do not leave Active Flows. This view hides merged Flow
records; moving focus to the left repo pane temporarily filters the visible
active rows to the selected repo, and returning focus to the middle pane
restores the global list. Normal Flow actions — phase launches,
attached-session resumes, auto-mode toggles, `i` issue and `p` PR opening,
`c`/`y` copies, and embedded Flow terminals — work from the visible active
Flow rows and their expanded phase rows.
