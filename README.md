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

The UI has two panes: repos on the left, content on the right. Press `enter`
or `tab` on a selected repo to focus the content pane; from the content pane,
`tab` or `bksp` returns focus to the repo pane. Press `f2` to open the
prompt-template editor for plan and Flow launch prompts. When a Flow embedded
terminal is open, `tab` cycles through the repo pane, Flow list, and terminal.
The active pane is highlighted with a blue border.

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
| `A` | Choose and persist the coding agent from a picker (`codex`, `codex-app`, or `claude`) |
| `V` | Choose and persist the startup default view (`1` through `9`) |
| `D` | Toggle destructive mode |
| `f` | Fetch all currently visible repos with `--prune` |
| `n` | Create a new local repo under the scan root, optionally creating a GitHub repo and wiring `origin` |
| `enter` | Switch focus to right pane |
| `tab` | Switch focus to right pane |
| `f2` | Edit prompt templates |
| `q`/`esc` | Quit |

**Right pane (content)**

| Key | Action |
|-----|--------|
| `↑`/`k` | Move selection up |
| `↓`/`j` | Move selection down |
| `/` | Fuzzy filter the current item list |
| `1`/`2`/`3`/`4`/`5`/`6`/`7`/`8`/`9` | Switch to worktrees / branches / stashes / history / reflog / sessions / plans / flows / active flows |
| `←`/`→`/`l` | Switch views in the right pane, wrapping between worktrees and active flows; use arrows or `l` in flows view because `h` toggles Flow headless/interactive command mode |
| `h` | Switch to the previous view outside flows view; toggle Flow headless/interactive command mode in flows view |
| `M` | Choose and persist model for the selected CLI agent in flows view |
| `E` | Choose and persist reasoning effort for the selected CLI agent in flows view |
| `enter` | Page diff in `less` (dirty worktree, dirty branch, stash, commit, or reflog entry), resume an inline worktree session, page a session transcript, or expand/collapse plan or Flow phases |
| `g` | Launch the next launchable phase for the selected Flow in flows view |
| `n` | Create a new worktree in worktrees view, a new branch in branches view, or a new Flow in flows view |
| `P` | Create a review worktree from a GitHub PR number or URL |
| `N` | Create a new worktree and launch the selected coding agent |
| `m` | Move or rename a linked worktree (worktrees view), or mark the selected Flow's GitHub PR as already merged after verifying it in GitHub (flows and active flows views) |
| `A` | Choose and persist the coding agent from a picker (`codex`, `codex-app`, or `claude`) |
| `V` | Choose and persist the startup default view (`1` through `9`) |
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
| `tab` | Cycle pane focus forward; with a Flow terminal open, cycles repo pane → Flow list → terminal |
| `bksp` | Switch focus to left pane |
| `f2` | Edit prompt templates |
| `q`/`esc` | Close a prompt/dialog or quit |

The right pane header shows the active mode. Press `1`–`9` or use arrow keys to
switch between worktrees, branches, stashes, history, reflog, sessions, plans,
flows, and active flows. Horizontal view switching wraps between worktrees and
active flows. Press `V` to choose which numbered view wtui opens on future
launches; leaving it unset keeps the built-in startup default of Flows. Press
`enter` or `tab` from the repo pane to focus the content pane. In the content
pane, `tab` or `bksp` switches focus back to the left repo pane. Press `f2` from normal
TUI views to edit the `[agent].plan_prompt` and `[flow_prompts]` templates;
Flow terminal input focus passes F2 through to the embedded agent.

When the left repo pane is focused, press `f` to run `git fetch --prune` for
the currently visible repos. Repo filtering limits the batch to the filtered
list captured when the key is pressed.

Press `n` in the left repo pane to create a new repository directly under the
resolved scan root. The form asks for a repo name, whether to create a GitHub
repo (checked by default), and public/private visibility (public by default).
Repo names must be one path segment: they cannot be empty, `.`, `..`, start
with `-`, contain path separators, or end with `-worktrees` (reserved for wtui
worktree directories).
wtui always creates the local Git repository first. When GitHub creation is
enabled, wtui then runs `gh repo create <name> --public|--private --source <path> --remote origin`;
`gh` must be installed and authenticated. If the GitHub step fails after local
creation succeeds, wtui keeps the local repo and reopens the form so submitting
again retries only the GitHub/origin setup against that existing local path.

### Worktrees view (mode 1)

Shows all worktree checkouts for the selected repo. The main (root) worktree
always appears first with a blue `[root]` annotation. wtui starts in Flow mode
by default unless `[ui].default_view` is set, but worktrees remain mode `1` and
keep the same numeric access.

Each row shows the branch name (or `(detached)` for detached HEAD), status indicators, and the worktree path:

- `✔` green: clean working tree
- `●` red: dirty — shows `N files +X/-Y` (lines added/deleted)
- `✗` red: stale — worktree directory no longer exists

Press `A` to choose `codex`, `codex-app`, or `claude` from a picker; wtui persists the choice to config.
In flows view, press `E` to choose the selected CLI agent's reasoning effort
for future launches.
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

Press `x` on a selected worktree to show captured agent sessions for that
worktree inline. While the inline session list is open, `up`/`down` move through
the sessions and `enter` resumes the selected session from its recorded `cwd` or
worktree path. Filtering worktrees, refreshing the worktree list, switching
modes, or changing repos closes the inline list. The full sessions view in mode
`6` remains repo-scoped and keeps transcript, summary, resume, and copy-id
actions when no embedded terminal is active.

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
opens the diff in `less -R`. `t` opens or attaches to a tmux/Zellij
session, `c` opens VSCode at the worktree path, and `a` launches the selected
coding agent for checked-out branch rows only. `f` runs `git fetch --prune`,
and `F` runs `git pull --ff-only` for branches that have a checked-out
worktree. `d` deletes non-worktree branches, with a force-retry prompt on
failure. Deletion requires destructive mode to be enabled first (`D`).

### Stashes view (mode 3)

Browse stashes for the selected repo. Long stash messages wrap to two lines (date + message start, then indented continuation). Use `↑`/`↓` to select a stash, `enter` to page its diff in `less -R`, `d` to drop the selected stash (with confirmation, requires destructive mode). The stash list scrolls when entries exceed the pane height.

### History view (mode 4)

Browse recent commits (up to 50) for the selected repo. Each row shows the commit hash, author, relative date, and subject. Use `enter` to page the full commit diff in `less -R`, `y` to copy the commit hash to clipboard, `t` to open or attach to a tmux/Zellij session, and `c` to open VSCode at the repo root.

### Reflog view (mode 5)

Browse HEAD reflog entries (up to 50) for the selected repo. Each row shows the abbreviated hash, selector (e.g. `HEAD@{0}`), relative date, and subject. Use `enter` to page the diff for that entry in `less -R` -- checkout entries with no tree changes page "No changes at this reflog entry". Use `y` to copy the entry hash to clipboard.

### Sessions view (mode 6)

Browse captured Claude Code and Codex sessions associated with the selected
repo. Rows show provider, branch, worktree, status, and summary. Use `/` to
filter sessions by provider, session ID, launch ID, branch, worktree, model,
status, or summary. Press `o` to page the normalized transcript in `less -R`,
`s` to page the selected summary, `r` to resume the selected provider session,
or `y` to copy the raw provider session ID. `enter` also pages the selected
transcript.

Resuming a CLI `codex` or `claude` session from the full sessions view opens a
runtime-only embedded terminal in the sessions pane. While embedded terminals
exist, the saved-session table is hidden and the pane shows a compact numbered
terminal header plus the active terminal screen. While the session terminal
right pane is focused, all keys except `tab` go directly to the active PTY
(including agent shortcuts like `ctrl+g`); after tabbing to the left pane, repo
pane keys operate normally. Press `ctrl+]` for wtui commands: `ctrl+] 1`-`9`
switches terminals, `ctrl+] l` opens a saved-session
picker, `ctrl+] d` detaches a tmux-backed terminal and opens a new external
terminal attached to that tmux session, `ctrl+] x` dismisses an exited terminal or confirms termination of a
running one, `ctrl+] q` or `ctrl+] esc` quits with cleanup, and
`ctrl+] ctrl+]` sends a literal `ctrl+]` to the agent.
When `tmux` is available at launch time, embedded CLI terminals start inside a
per-launch tmux session so detach can close wtui's embedded client while the
agent keeps running in tmux. After detach, wtui opens the reattach command with
`$TERMINAL`, then `[terminal].command`, then the macOS Terminal AppleScript
fallback when available. Active tmux/Zellij clients and installed inactive
tmux/Zellij commands are not used for this handoff. If no external terminal
transport is available, wtui still leaves the agent detached in tmux and reports
the handoff error. If `tmux` is unavailable, wtui keeps the direct embedded PTY
behavior and `ctrl+] d` reports that detach is unavailable.
Quitting wtui from anywhere while embedded terminals are still running asks for
confirmation and terminates them first. Terminate/quit cleanup kills tmux
sessions created by that embedded launch; detached tmux sessions are no longer
owned by wtui and are not prompted for on quit. Embedded terminals are not
restored after wtui restarts.

Session data is stored under the user state directory by default:
`$XDG_STATE_HOME/wtui/sessions/v1`, or
`~/.local/state/wtui/sessions/v1` when `XDG_STATE_HOME` is unset. Transcripts
may contain secrets or private prompts; wtui keeps them outside repositories and
uses restrictive file permissions for created session files. Provider session IDs
are stored in hashed directory names instead of raw path components.
When resuming a session, wtui runs the provider resume command from the recorded
session `cwd` when present, falling back to the captured worktree path, while
preserving the stored worktree metadata for subsequent hooks. `codex-app`
resumes keep using the existing macOS deep-link path rather than an embedded
terminal. Sessions missing a provider session ID cannot be resumed; wtui reports
this in the status line instead of starting a fresh provider session. Hook
payloads without a usable session ID are rejected at capture time, so no
unusable session records are stored.

### Plans view (mode 7)

Browse saved agent plans for the selected repo. Rows show status, branch, phase
progress (`completed/total`), the updated date, and the title. Use `/` to filter
plans by title, summary, status, branch, worktree basename, provider, session
ID, launch ID, and phase titles/statuses. Press `x` to expand or collapse
the selected plan's phase rows, `o` to page the plan Markdown in `less -R`,
`e` to edit the plan Markdown, and `y` to copy the plan Markdown path. The edit
action opens `[editor].command` when configured, otherwise `$EDITOR`, and
refreshes the plans pane when that command exits; use wait flags such as
`code --wait` for GUI editors that detach by default. Press `a` to edit launch
instructions for the selected plan or selected phase, then `enter` to launch the
selected agent or `esc` to cancel; blank instructions are rejected. `enter`
still toggles phase rows, and `i` still opens plan launch instructions as
compatibility aliases.

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

# Record per-phase progress. Phase ids are trimmed and lowercased, so
# re-running phase set with the same logical id updates the phase in place.
wtui plan phase set --plan-id "$PLAN_ID" --phase-id store --title "Store" --status completed --order 1

# Read plans back.
wtui plan list --repo-path "$REPO" --json   # requires --json in v1
wtui plan read --plan-id "$PLAN_ID"          # prints Markdown only
```

The plan state root is resolved with this precedence: `--state-root` >
`WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` > `[sessions].root` > the user
state default. CLI-launched agents get `WTUI_SESSION_STATE_ROOT`,
`WTUI_PLAN_STATE_ROOT`, and `WTUI_FLOW_STATE_ROOT` set to the same resolved
artifact root. Omitted metadata is filled first from `WTUI_AGENT`,
`WTUI_LAUNCH_ID`, `WTUI_REPO_PATH`,
`WTUI_WORKTREE_PATH`, `WTUI_BRANCH`, and `WTUI_COMMIT`; for new plans, and for
updates that provide a repo or worktree location, wtui also resolves best-effort
repo, worktree, branch, and start commit metadata from git. `codex-app`
launches use macOS `open`, so they do not inherit `WTUI_*` shell environment
variables; wtui uses the repo path as the deep-link project path and includes
worktree, state-root, plan, and flow values as prompt-only launch metadata.
That metadata includes copyable `wtui plan list --json --state-root ...` and
`wtui flow list --json --state-root ...` examples that show where to pass the
state root for subsequent plan and flow commands. The `wtui-plan-persist` skill
instructs agents on when and how to save plans. Its canonical source lives in
`agent-skills/wtui-plan-persist/`, which is intentionally outside Codex and
Claude's repo auto-discovery directories so it can be symlinked into user-level
skill dirs for use across repos. v1 has no TUI plan deletion.

### Flows view (mode 8)

Browse persisted Flow records for the selected repo. Rows show status, branch
or worktree basename, phase progress plus the current phase state, optional
Issue reference, linked plan ID when present, PR number or label, updated date,
and title. The column order is Status, Branch, Phase, Issue, Plan, PR, Updated,
Title. Use `/` to filter by title, instructions, status, branch, worktree
basename, plan metadata, issue metadata, PR metadata,
phase titles/statuses/summaries, and linked session metadata. Press
`n` to create a new Flow with one form for title, multiline instructions, and
optional base ref plus Headless and Plan Now checkboxes; use `alt+enter` for
instruction newlines. Plan Now is checked by default and immediately launches
the initial Plan phase after creating the Flow. Uncheck it to create a parked
Flow with its instructions, worktree, branch, and start commit saved; the ready
Plan phase can be launched later from the Flow row. On a Flow row, `enter`
expands or collapses phase detail rows; `o` pages the linked plan body in
`less -R`, and wtui shows a status message when the selected Flow has no linked
plan. With
destructive mode enabled (`D`), `d` deletes only the selected top-level Flow
record under the Flow artifact store; it does not remove repositories,
worktrees, branches, checked-out code, linked plans, sessions, transcripts, or
active embedded terminals. Expanded phase rows cannot be deleted with this
action. On a Flow row or an expanded phase row, `enter` expands or collapses
the phase list. Press
`i` on a Flow row with linked GitHub issue metadata to open the issue in the
browser. Press
`p` on a Flow row with linked PR metadata to open the PR in the browser. Press
`g` to launch the first launchable phase in the selected Flow's
canonical phase order. This action
uses the selected Flow, so a highlighted pending phase row can still launch an
earlier ready sibling, and nothing is persisted when no phase is launchable.
Press `c` to copy the selected Flow ID, and press `y` to copy the selected Flow
worktree path, from either a Flow row or one of its expanded phase rows. Press
`r` on an expanded phase row with an
attached provider session to resume that session; CLI resumes are recorded as a
fresh Flow phase launch attempt, while `codex-app` resumes navigate to the
existing app thread without extra launch tracking. Press `a` on a Flow row or
expanded phase row to toggle per-Flow auto mode, which is on by default for new
Flows and persisted on that Flow record. Flows created before this field existed
remain manual until auto mode is toggled on. Press `m` on an eligible Flow row
when its recorded GitHub PR was merged manually in GitHub; wtui verifies the PR
is merged with `gh`, records the merge commit and timestamp, marks the Merge
phase completed, and hides the Flow from active lists without launching a Merge
phase agent. When auto mode is on, wtui runs an always-on, all-repos advance
poll that detects live completed-phase transitions and launches the next ready
non-merge phase in that Flow even when another view is active. Auto-launched CLI
phases are always headless and do not change the current view or focus. If the
completed phase still has an embedded Flow terminal, wtui waits until that
terminal exits normally and auto-closes before launching the next phase. A 3 s
status message announces auto-launches, `needs_attention`, and merge-ready
transitions unless another status message is active. Skipped, blocked,
needs-attention, failed-launch, or missing-PR-metadata states do not
auto-launch. Automation stops before Merge: if Autoreview completes and Merge
becomes ready, wtui keeps auto mode on and requires the existing manual Merge
launch.

### Active Flows view (mode 9)

Browse active Flow records across all repos. This view hides merged Flow records;
moving focus to the left repo pane temporarily filters the visible active rows to
the selected repo, and returning focus to the middle pane restores the global
list. Normal Flow actions, phase launches, attached-session resumes, auto-mode
toggles, linked-issue opening with `i`, linked-PR opening with `p`, `c` Flow ID
copy, `y` worktree path copy, and embedded Flow terminals work from the visible
active Flow rows and their expanded phase rows.

Flow headless mode is on by default:
selected CLI `codex` and `claude` phase launches run in a runtime-only embedded
terminal inside the flows pane. Press `h` to choose the CLI command mode:
headless runs `codex exec` or `claude --print`, while
headless off runs interactive `codex` or `claude` in the same embedded Flow
terminal. Headless-off Flow launches prefill the phase prompt without
submitting it, then focus the Flow terminal in input mode so you can review or
edit it before pressing enter. Headless launches keep focus on the Flow list.
Creating a new Flow has its own default-on Headless checkbox for the initial
Plan launch; uncheck it for an interactive initial Plan launch. That checkbox
is ignored when Plan Now is off and does not change the selected-phase `h`
setting. Manual phase launches, auto-launched phases, and new Flow Plan
launches all use the configured agent and that agent's configured model and
effort, but the `h` toggle applies only to manual phase launches; AutoMode CLI
launches are always headless. Press `M` to choose the selected CLI agent's
model; press `E` to choose its reasoning effort. The shortcut pane shows the
current values. Codex CLI
launches use `--model <model>` and `--config
model_reasoning_effort=<effort>`; Claude launches use `--model <model>` and
`--effort <effort>`. Session resumes do not receive model or effort flags.
`codex-app` always uses the external deep-link route and keeps app-side/default
model and reasoning. Embedded headless output is readable terminal text, not raw
JSON events: `codex exec` streams its progress
directly, while `claude --print` is run with `--output-format stream-json
--include-partial-messages` and wtui translates those events into readable lines
(thinking, tool calls and results, the final answer streamed token-by-token, and
a closing summary) so a Claude phase shows live progress as it works instead of a
blank terminal. While a Flow terminal is open,
the Flow list uses a smaller top panel and the terminal uses a bottom panel;
`tab` cycles focus through the repo pane, Flow list, and Flow terminal. Manually
tabbing into Flow terminal focus starts in wtui command mode: `left`/`right`
cycle Flow terminals, `1`-`9` switches by number, `x` closes, `d` detaches to
tmux when available and opens the detached session in an external terminal,
`q`/`esc` quits, unknown ordinary keys do not pass through to the PTY, `ctrl+]`
sends a literal `ctrl+]`, and `i` enters terminal input mode. In input
mode, keys pass through to the PTY (including agent shortcuts like `ctrl+g`)
and `ctrl+]` returns to command mode. When
Implementation is still gated by Plan Review, wtui reports the Plan Review state
and notes instead of launching. When PR Creation is complete but structured PR
metadata is missing, Autoreview remains pending and the Flow row shows
`autoreview:missing-pr`.
Expanded phase rows group child implementation phases directly under
Implementation.

Flow rows also surface recoverable partial states so they are not confused with
ordinary empty or pending work. A saved Flow with no branch/worktree metadata
shows `recover-worktree`, a running phase with a recorded launch but no attached
session yet shows `await-session`, a phase with an attached session whose
launch ID does not match the phase's launch attempts shows `session-mismatch`,
a running phase whose latest attached session has ended shows `ended-session`,
and an attached session that lacks a provider session ID shows
`missing-session-id`. A pending Autoreview phase whose PR Creation predecessor
completed without structured PR metadata shows `missing-pr`.

When an expanded phase row shows `await-session` or `ended-session`, and no
running or starting embedded Flow terminal is attached to that same Flow phase,
the selected phase row exposes `x reset ready`. Confirming the prompt removes
the newest stale launch attempt, removes ended session records tied to that
launch, persists the phase as `pending`, and lets wtui derive the phase back to
`ready`. `wtui flow phase reset` performs the same recovery from the CLI. Live
or unknown-status attached sessions anywhere on the merged logical phase, and
session launch mismatches, are rejected. This is wtui recovery for an abandoned
launch attempt or ended provider session, not a new agent transition; `ready`
still cannot be set through `wtui flow phase set`.

Flows are task-centric workflow records stored beside sessions and plans under
`<sessions root>/flows/<flow-id>/meta.json`. The TUI can create a new Flow and
can record a launch for the next launchable phase; agents still perform normal
phase progression through the `wtui flow` CLI:

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

# Record optional GitHub issue metadata when the task references an issue.
wtui flow issue set --flow-id "$FLOW_ID" \
  --provider github \
  --number 123 \
  --url "https://github.com/owner/repo/issues/123"

# Record common phase outcomes without hand-assembling --status. These commands
# print JSON with the updated phase and the next actionable phase state. For
# Plan Review, complete defaults to approved, needs-attention defaults to
# changes_requested, and block defaults to blocked unless --outcome is supplied.
# Autoreview defaults are passed, needs_attention, and blocked.
wtui flow phase complete --flow-id "$FLOW_ID" --phase-id plan --summary "Saved plan"
wtui flow phase needs-attention --flow-id "$FLOW_ID" --phase-id plan-review \
  --notes "Revise the rollout section"
wtui flow phase block --flow-id "$FLOW_ID" --phase-id implementation \
  --notes "Waiting on review"
wtui flow phase reset --flow-id "$FLOW_ID" --phase-id implementation

# The lower-level phase set command remains available for explicit status,
# outcome, summary, and notes updates. approved_with_concerns,
# changes_requested, and blocked Plan Review outcomes require --notes.
wtui flow phase set --flow-id "$FLOW_ID" --phase-id plan-review \
  --status completed --outcome approved_with_concerns --notes "Watch rollout risk"

# Split Implementation into ordered child phases. Re-running the same command
# updates the stable child phase without duplicating it.
wtui flow phase add-child --flow-id "$FLOW_ID" \
  --parent-phase-id implementation \
  --phase-id implementation-api \
  --title "API integration" \
  --order 10

# Record structured PR metadata after PR Creation opens or updates a PR.
wtui flow pr set --flow-id "$FLOW_ID" \
  --provider github \
  --number 123 \
  --url "https://github.com/owner/repo/pull/123" \
  --head "$FLOW_BRANCH" \
  --base main \
  --status open

# Record structured merge metadata after the explicit merge action succeeds.
wtui flow phase set --flow-id "$FLOW_ID" \
  --phase-id merge \
  --status completed \
  --outcome merged \
  --summary "Merged PR at $MERGE_COMMIT."

wtui flow merge set --flow-id "$FLOW_ID" \
  --status merged \
  --commit "$MERGE_COMMIT" \
  --merged-at "2026-06-08T15:04:05Z"
```

When a Flow is linked to a saved plan, transitioning a Flow phase to `completed`
also marks a matching saved-plan phase with the same normalized phase ID as
`completed`. Missing saved-plan phases are ignored. If that sync fails, wtui
marks the Flow phase `needs_attention` and reports the persistence error.
Repeating `completed` for an already-completed Flow phase preserves that
completed state even if the linked-plan sync later fails.
The TUI-owned manual merge shortcut still treats linked-plan sync failure as
recoverable: it restores the previous PR status, clears terminal merge
metadata, marks the Merge phase `needs_attention`, and keeps the Flow visible
for repair.

Child implementation phases gate downstream readiness in phase order: review
loop and PR creation remain pending until required implementation children are
completed or explicitly skipped with notes. Flow phase launch prompts stay
minimal: Plan Review and Implementation point to the saved plan artifact, while
Review Loop and PR Creation include only the worktree, branch, and start commit
metadata needed to inspect the changes. Built-in prompts tell Plan to produce
only a plan, Plan Review to use the review-loop skill with max 6 loops,
Implementation to use the `commit` skill, Review Loop to use the review-loop
workflow with goal `review-and-revise` and `commit` when revisions are made,
PR Creation to use the `ship` skill, and Autoreview to use `ship` when fixes
require commits or pushes without embedding phase-restart recipes. All Flow
phase launch prompts also end with:
`After completing this phase goal, mark this Flow phase done with wtui-flow.`
Use `wtui flow phase restart` to rerun a blocked or needs-attention phase as
`running`; if notes are omitted, wtui records a standard rerun note.

For example, after addressing Autoreview findings:

```bash
wtui flow phase restart --flow-id "$FLOW_ID" --phase-id autoreview
```

Autoreview is ready only after PR Creation is complete and
`wtui flow pr set` has recorded provider, PR number, URL, head branch, and base
branch metadata. Merge stays an explicit phase:
agents must record both the Merge phase update and structured merge metadata
through `wtui flow merge set`; `--status merged` requires existing PR metadata,
a merge commit, and an RFC3339 merge timestamp. If merge is blocked, record a
blocked Merge phase with notes before setting structured merge status to
`blocked`. The canonical phase transition table, derived-readiness rules, and
the on-disk compatibility story are documented in
[docs/flow-phases.md](docs/flow-phases.md).

The flow state root is resolved with this precedence: `--state-root` >
`WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` >
`[sessions].root` > the user state default. In TUI startup,
`WTUI_FLOW_STATE_ROOT`, `WTUI_PLAN_STATE_ROOT`, or `WTUI_SESSION_STATE_ROOT`
relocates the shared artifact root for sessions, plans, and flows.

Flow-launched agents should use the canonical `wtui-flow` skill source at
`agent-skills/wtui-flow/`. Ad hoc planning sessions that need to create a new
Flow from the current task or an already-written plan should use
`wtui-flow-create` from `agent-skills/wtui-flow-create/`. Install or symlink
both skills beside `agent-skills/wtui-plan-persist/` in the user-level skill
directory for your agent, such as `~/.codex/skills/wtui-flow` and
`~/.codex/skills/wtui-flow-create` for Codex or the equivalent Claude skills
directory. `wtui-flow` activates when `WTUI_FLOW_ID` and `WTUI_FLOW_PHASE_ID`
are present, reads the active flow before updates, and uses the implemented
`wtui flow` and `wtui plan` commands for persistence. `wtui-flow-create` works
outside a Flow-launched session by calling `wtui flow create`, optionally saving
and linking an existing plan. In v1, that import persists the Flow and linked
plan artifacts but does not attach the current ad hoc provider session to the
Flow; future phase launches and resumes are tracked normally.

## Configuration

wtui reads an optional TOML config file before scanning repositories:

```text
$XDG_CONFIG_HOME/wtui/config.toml
~/.config/wtui/config.toml
```

Unknown sections and keys are ignored for version compatibility; malformed
TOML, wrong types for known keys, and invalid known values still fail startup.

Example:

```toml
[scan]
root = "~/projects"
max_depth = 2

[agent]
command = "codex"
codex_model = "gpt-5.5"
claude_model = "claude-sonnet-5"
codex_reasoning_effort = "high"
claude_reasoning_effort = "max"
plan_prompt = "Implement the saved wtui plan {title} (ID: {plan_id}) at {plan_path}. Read the plan file, then begin implementation."

[ui]
default_view = 8

[flow_prompts]
implementation = "Implement {plan_path} from {worktree_path} for issue {issue_number}, then use the commit skill before completing."
pr_creation = "Use the ship skill for {branch}, then record PR metadata for flow {flow_id}."

[sessions]
root = "~/.local/state/wtui/sessions/v1"
copy_raw_transcripts = false

[bootstrap]
timeout_seconds = 120

[[bootstrap.hooks]]
repo_path = "~/projects/wtui"
script = ".wtui/bootstrap"
```

`WORKTREE_ROOT` overrides `[scan].root` when both are set. The scan root is
cleaned before scanning; explicit relative roots preserve relative repo paths
for compatibility. The same root is resolved to an absolute path when used as
the parent directory for left-pane repo creation.
`[agent].codex_reasoning_effort` and `[agent].claude_reasoning_effort`
configure provider-specific effort for new CLI agent launches; empty or
`default` keeps provider defaults. `[agent].codex_model` supports `default` and
`gpt-5.5`; `[agent].claude_model` supports `default`, `claude-opus-4-8`,
`claude-sonnet-5`, and `claude-fable-5`. Empty or `default` omits the model
override and keeps provider defaults. `[ui].default_view` accepts `1` through `9`
and controls the startup view; omitting it keeps the built-in Flows default.
`[agent].plan_prompt` customizes the
editable instructions shown before launching an agent from the plans pane, while
`[flow_prompts]` customizes Flow phase launch templates. wtui appends
`After completing this phase goal, mark this Flow phase done with wtui-flow.`
to configured Flow templates unless the template already ends with that exact
standalone instruction.
`[editor].command` customizes the editor used by the plans pane edit action.
See [docs/config.md](docs/config.md) for the full config reference, including
sessions storage, bootstrap hook settings, terminal settings, and parsed
foundation fields for provider, launch, and agent settings.

| Env var | Default | Description |
|---------|---------|-------------|
| `WORKTREE_ROOT` | `[scan].root` or `~/dev` | Root directory to scan for git repos and create new repos under; explicit relative paths are preserved for scanned repo identity and resolved from the current working directory for repo creation, depth defaults to 2 and can be reduced with `[scan].max_depth` |
| `TERMINAL` | unset | Terminal command to use when `t` opens a worktree outside tmux/Zellij |
| `WTUI_SESSION_STATE_ROOT` | `[sessions].root` or user state default | Session hook storage root; normally set automatically for agents launched by wtui |
| `WTUI_PLAN_STATE_ROOT` | `WTUI_SESSION_STATE_ROOT`, `[sessions].root`, or user state default | Saved-plan artifact root for `wtui plan`; set automatically for agents launched by wtui. In the TUI it relocates the whole artifact root, moving sessions, plans, and flows |
| `WTUI_FLOW_STATE_ROOT` | `WTUI_PLAN_STATE_ROOT`, `WTUI_SESSION_STATE_ROOT`, `[sessions].root`, or user state default | Flow artifact root for `wtui flow`. In the TUI it has highest precedence for the shared sessions/plans/flows artifact root |

### Agent session hooks

CLI agents launched from wtui with `a`, `N`, Flow `g`, or
session resume `r` are wired automatically: wtui passes Claude Code or Codex a
session-end hook that calls the current wtui binary, and it exports `WTUI_*`
metadata so hook records can be associated with the repo, worktree, branch, and
launch. `codex-app` opens via macOS deep link instead; wtui scrubs inherited
`WTUI_*` from `open` and includes prompt-only launch metadata with copyable
`--state-root` command examples. New `codex-app` threads use the repo path for
Codex App project identity when wtui knows it, while the selected worktree
remains available in the prompt metadata.

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
