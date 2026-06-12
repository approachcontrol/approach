# wtui Configuration

wtui reads an optional TOML config file before scanning repositories.

## Config Path

wtui looks for config in this order:

1. `$XDG_CONFIG_HOME/wtui/config.toml`
2. `~/.config/wtui/config.toml`

The file is optional. If it does not exist, wtui starts with built-in defaults.
If a config file exists but cannot be read or parsed, startup fails before
repository scanning. wtui only falls through to the next path when the earlier
path does not exist.

## Precedence

Environment variables remain the highest-precedence settings where they already
exist:

| Setting | Highest precedence | Config fallback | Built-in default |
|---------|--------------------|-----------------|------------------|
| Scan root | `WORKTREE_ROOT` | `[scan].root` | `~/dev` |
| Plan editor command | `[editor].command` | `EDITOR` | unset |
| Terminal command | `TERMINAL` | `[terminal].command` | platform fallback |
| Coding agent | none | `[agent].command` | unset |
| Plan launch prompt | none | `[agent].plan_prompt` | built-in plan implementation prompt |
| Flow phase launch prompts | none | `[flow_prompts]` | built-in Flow phase prompts |
| TUI artifact root | `WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` | `[sessions].root` | `$XDG_STATE_HOME/wtui/sessions/v1` or `~/.local/state/wtui/sessions/v1` |
| Session hook root | `--state-root` > `WTUI_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root |
| Plan state root | `--state-root` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root (`<root>/plans/...`) |
| Flow state root | `--state-root` > `WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root (`<root>/flows/...`) |
| Bootstrap hook timeout | none | `[bootstrap].timeout_seconds` or hook override | `120` seconds |

`[scan].root` and `[sessions].root` support `~` and `~/...` expansion.
Session roots must resolve to absolute paths so captured transcripts stay out of
repositories.
`WORKTREE_ROOT` is passed through as provided by the environment.

## Example

```toml
[scan]
root = "~/dev"
max_depth = 2

[editor]
command = "code"

[terminal]
command = "wezterm start"

[provider]
name = "github"

[launch]
prefer_multiplexer = true

[agent]
command = "codex"
plan_prompt = "Implement the saved wtui plan {title} (ID: {plan_id}) at {plan_path}. Read the plan file, then begin implementation."

[flow_prompts]
plan = "Produce a plan only for: {instructions}"
implementation = "Implement {plan_path} in {worktree_path}, then use the commit skill before completing."
review_loop = "Use review-loop for {branch}; use commit if revisions are made."
pr_creation = "Use ship for {branch}; record PR metadata for flow {flow_id}."
autoreview = "Autoreview {pr_url}; use ship when fixes require commits or pushes."

[sessions]
root = "~/.local/state/wtui/sessions/v1"
copy_raw_transcripts = false

[bootstrap]
timeout_seconds = 120

[[bootstrap.hooks]]
repo_path = "~/dev/wtui"
script = ".wtui/bootstrap"

[[bootstrap.hooks]]
repo_path = "~/dev/client-api"
script = "~/bin/bootstrap-client-api"
timeout_seconds = 300
```

## Sections

### `[scan]`

Controls repository discovery.

| Key | Type | Description |
|-----|------|-------------|
| `root` | string | Directory to scan for git repositories. |
| `max_depth` | integer | Scan depth below `root`; `1` scans immediate children, `2` also scans one level deeper. |

When `max_depth` is omitted or set to `0`, wtui uses the scanner default of `2`.
Values greater than `2` behave like `2`.

### `[editor]`

Configures the editor used by the plans pane `e` action. The selected plan's
`plan.md` path is appended to the command, and the plans pane refreshes after
the configured command exits. Use wait flags such as `code --wait` for GUI
editors that detach by default. When this setting is omitted, wtui falls back
to `$EDITOR`.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Shell-style editor command, such as `code --wait`, `vim`, or `vim -c "set ft=markdown"`. |

### `[terminal]`

Configures the external terminal fallback used by the `t` action and by
detached agent-launch scripts. Active tmux/Zellij sessions still take
precedence, and `TERMINAL` still overrides this setting.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Terminal command or supported macOS GUI app alias. |

Examples:

```toml
[terminal]
command = "wezterm start"
```

```toml
[terminal]
command = "iTerm"
```

On macOS, supported GUI aliases are `Terminal`, `Terminal.app`, `iTerm`,
`iTerm.app`, `iTerm2`, and `iTerm2.app`. Terminal aliases use the built-in
Terminal transport. iTerm aliases use AppleScript so both plain worktree
terminals and detached agent scripts open in iTerm.

Other command values are treated as whitespace-separated CLI terminal commands
when the first field exists on `PATH`; configured arguments are preserved as
separate argv entries and agent launches append `-e sh -c <script>`. Shell
quoting is not interpreted in this setting. On macOS, an unsupported GUI app
name can open a plain worktree terminal with `open -a <app> <path>`, but it
cannot run detached agent scripts. Use a supported GUI alias or a CLI terminal
command for agent launches.

### `[provider]`

Parsed for future provider-specific features.

| Key | Type | Description |
|-----|------|-------------|
| `name` | string | Future provider name, such as `github`. |

### `[launch]`

Parsed for future launch behavior.

| Key | Type | Description |
|-----|------|-------------|
| `prefer_multiplexer` | boolean | Future launch preference for tmux/Zellij behavior. |

### `[agent]`

Stores the selected coding agent for interactive launches. Pressing `A` in wtui
opens an agent picker for `codex`, `codex-app`, or `claude` and updates this
value immediately, creating the config file if needed.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Supported values: `codex`, `codex-app`, or `claude`. |
| `plan_prompt` | string | Optional one-line template for the editable instructions opened by `i` in the plans pane. Supports `{title}`, `{plan_id}`, `{plan_path}`, `{repo_path}`, and `{worktree_path}`. Unknown placeholders remain literal. Blank or omitted uses the built-in prompt. |

### `[flow_prompts]`

Optional templates for Flow phase launch prompts. Blank or omitted keys use
the built-in prompt for that phase. Unknown placeholders remain literal.

| Key | Type | Description |
|-----|------|-------------|
| `plan` | string | Template for the initial Plan phase launch. |
| `plan_review` | string | Template for Plan Review. |
| `implementation` | string | Template for Implementation. |
| `review_loop` | string | Template for Review Loop. |
| `pr_creation` | string | Template for PR Creation. |
| `autoreview` | string | Template for Autoreview. |
| `merge` | string | Template for Merge. |
| `generic` | string | Template for non-standard Flow phase IDs. |

Supported Flow placeholders are `{flow_id}`, `{flow_title}`,
`{instructions}`, `{phase_id}`, `{phase_title}`, `{plan_id}`, `{plan_path}`,
`{plan_body}`, `{repo_path}`, `{worktree_path}`, `{branch}`, `{commit}`,
`{base_ref}`, `{pr_provider}`, `{pr_number}`, `{pr_url}`, `{pr_head}`,
`{pr_base}`, and `{pr_status}`. Standard Plan Review, Implementation, Review
Loop, PR Creation, Autoreview, and Merge launches do not pre-read the linked
plan body, so `{plan_body}` is empty for those built-in phase types unless a
future phase path explicitly supplies it.

### `[sessions]`

Controls portable agent-session storage. Session metadata and normalized
transcripts are stored outside repositories by default. Each provider session is
stored under a hashed session directory, with the raw provider session ID kept in
`meta.json`.

| Key | Type | Description |
|-----|------|-------------|
| `root` | string | Optional absolute state root for session files. Supports `~` expansion. |
| `copy_raw_transcripts` | boolean | Whether hook ingestion also preserves provider-native transcript JSONL as `raw.jsonl`. Defaults to `false`. |

When `root` is omitted, wtui uses `$XDG_STATE_HOME/wtui/sessions/v1`, or
`~/.local/state/wtui/sessions/v1` when `XDG_STATE_HOME` is unset.
Relative roots other than `~`/`~/...` fail config parsing.

`[sessions].root` doubles as the **agent-artifact root**: sessions, saved plans,
and Flow records are stored under `<root>/sessions/...`,
`<root>/plans/<plan-id>/`, and `<root>/flows/<flow-id>/`. There is no separate
plans or flows config in v1. **Moving or cleaning the sessions root therefore
also moves or removes saved plans and Flow records.**

## Saved Plans

Agents persist plans explicitly through the `wtui plan` subcommands; plans are
not captured from provider hooks in v1. Each plan is stored as
`<artifact-root>/plans/<plan-id>/meta.json` plus `plan.md`, with the same
restrictive permissions (`0700` directories, `0600` files) and atomic writes as
sessions. They appear in the TUI plans pane (mode `7`).
Use `e` in the plans pane to edit the selected `plan.md` with `[editor].command`
or `$EDITOR`; missing editor commands are shown in the TUI status bar. The plans
pane refreshes when the configured editor command exits.

```bash
# Save or update (reuse --plan-id) a plan; Markdown comes from --file or stdin.
# Prints only the plan_id.
printf '%s' "$PLAN_MD" | wtui plan save --title "Persist plans" [--plan-id ID] \
    [--summary TEXT] [--status STATUS] [--source SOURCE] [--provider PROVIDER] \
    [--session-id ID] [--launch-id ID] [--repo-path PATH] [--worktree-path PATH] \
    [--branch BRANCH] [--commit HASH] [--file PATH] [--state-root PATH]

wtui plan phase set --plan-id ID --phase-id ID --title TITLE --status STATUS [--order N] [--state-root PATH]
wtui plan list [--repo-path PATH] [--state-root PATH] --json   # --json required in v1
wtui plan read --plan-id ID [--state-root PATH]                # prints Markdown only
```

Plan statuses: `draft`, `approved`, `in_progress`, `completed`, `blocked`,
`superseded`. Phase statuses: `pending`, `in_progress`, `completed`, `blocked`,
`skipped`. Phase IDs are normalized (trimmed and lowercased) before matching,
so re-running `phase set` with the same logical `--phase-id` -- including case
or whitespace variants -- updates that phase in place instead of adding a
duplicate row, and repairs records that already contain duplicates. `plan_id` must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`; when
omitted, wtui generates `YYYYMMDDTHHMMSSZ-<title-slug>` with a `-2`, `-3`, …
suffix on collision.

`save` always replaces Markdown and title from the command (both required), and
updates `status`, `source`, `summary`, and repo/session metadata only when
supplied; otherwise it preserves the stored values, `created_at`, and recorded
phases. A body-only re-save keeps the existing status.

The plan state root is resolved as: `--state-root` > `WTUI_PLAN_STATE_ROOT` >
`WTUI_SESSION_STATE_ROOT` > `[sessions].root` > the user state default. The
`wtui plan` commands may load config to resolve the root but never scan repos or
start the TUI. Omitted metadata is filled from `WTUI_AGENT` (provider),
`WTUI_LAUNCH_ID`, `WTUI_REPO_PATH`, `WTUI_WORKTREE_PATH`, `WTUI_BRANCH`, and
`WTUI_COMMIT`; for new plans, and for updates that provide a repo or worktree
location, wtui also resolves best-effort repo, worktree, branch, and commit
metadata from git. `codex-app` launches do not inherit `WTUI_*` shell
environment variables because wtui opens a macOS deep link; wtui uses the repo
path as the deep-link project path and includes worktree, state-root, plan, and
flow values as prompt-only launch metadata. That metadata includes copyable
`wtui plan list --json --state-root ...` and
`wtui flow list --json --state-root ...` examples that show where to pass the
state root for subsequent plan and flow commands. The
`wtui-plan-persist` skill instructs agents on when and how to save plans; its
canonical source lives in
`agent-skills/wtui-plan-persist/` for symlinking into user-level Codex/Claude
skill directories.

## Flows

Flow records are task-centric workflow records created by the TUI or explicitly
through `wtui flow`. Each record is stored as
`<artifact-root>/flows/<flow-id>/meta.json`, with restrictive permissions
(`0700` directories, `0600` files) and atomic writes. They appear in the TUI
flows pane (mode `8`), which is the startup default. The pane shows linked plan
IDs when present; press `n` to create a new Flow. On a Flow row, `enter`
expands or collapses read-only phase detail rows; `o` opens the linked plan
body from the selected Flow. On an expanded phase row, `enter` launches the
configured agent for that selected ready phase. Headless mode is on by default:
selected CLI `codex` and `claude` phase launches run in an embedded terminal
inside the flows pane. Press `h` to choose the CLI command mode: headless runs
`codex exec` or `claude --print`, while headless off runs interactive `codex` or
`claude` in the same embedded Flow terminal. The same command mode applies to
the initial Plan launch when creating a new Flow. `codex-app` remains
URL/deep-link based and launches externally. Press `r` to resume an attached
provider session from the selected phase row; CLI resumes open in runtime-only
embedded PTYs in the flows pane, while `codex-app` resumes navigate externally.
While a Flow
terminal is open, `tab` switches focus between the Flow list and terminal;
terminal focus forwards ordinary keys to the PTY and keeps `ctrl+g` prefix
commands available. Embedded headless output is rendered as readable terminal
text rather than raw provider event JSON; `codex exec` streams progress while it
runs, whereas `claude --print` only prints its result once the run completes.
Expanded rows
group child implementation phases directly under Implementation. New launches
record a launch ID and Flow/plan environment metadata for the agent; CLI
phase-session resumes also record a fresh launch ID, while `codex-app` resumes
navigate to the existing app thread without additional launch tracking. Other
Flow mutation remains CLI/agent-driven in v1.

```bash
# Create a flow. --repo-path must be absolute, instructions are required, and
# --json is required in v1.
wtui flow create --title "Ship saved plans" \
    --instructions "Plan, implement, review, open a PR, and merge." \
    --repo-path "$REPO" [--worktree-path PATH] [--branch BRANCH] \
    [--base-ref REF] [--commit HASH] [--state-root PATH] --json

# You may also read instructions from a file.
wtui flow create --title "Ship saved plans" \
    --instructions-file ./instructions.md --repo-path "$REPO" --json

wtui flow list [--repo-path PATH] [--state-root PATH] --json
wtui flow read --flow-id ID [--state-root PATH]
wtui flow phase complete --flow-id ID --phase-id ID \
    [--outcome OUTCOME] [--summary TEXT] [--notes TEXT] [--state-root PATH]
wtui flow phase block --flow-id ID --phase-id ID \
    [--outcome OUTCOME] [--summary TEXT] [--notes TEXT] [--state-root PATH]
wtui flow phase needs-attention --flow-id ID --phase-id ID \
    [--outcome OUTCOME] [--summary TEXT] [--notes TEXT] [--state-root PATH]
wtui flow phase restart --flow-id ID --phase-id ID \
    [--notes TEXT] [--state-root PATH]
wtui flow phase set --flow-id ID --phase-id ID --status STATUS \
    [--outcome OUTCOME] [--summary TEXT] [--notes TEXT] [--state-root PATH]
wtui flow phase add-child --flow-id ID --parent-phase-id implementation \
    --phase-id ID --title TITLE --order N [--state-root PATH]
wtui flow plan set --flow-id ID --plan-id ID [--plan-path ABSOLUTE_PATH] [--state-root PATH]
wtui flow pr set --flow-id ID --provider github --number N --url URL \
    --head HEAD_BRANCH --base BASE_BRANCH [--status STATUS] [--state-root PATH]
```

When a Flow is linked to a saved plan, transitioning a Flow phase to `completed`
also marks a matching saved-plan phase with the same normalized phase ID as
`completed`. Missing saved-plan phases are ignored. If that sync fails, wtui
marks the Flow phase `needs_attention` and reports the persistence error.
Repeating `completed` for an already-completed Flow phase preserves that
completed state even if the linked-plan sync later fails.

Flow IDs use the same safe single-path-segment shape as plans:
`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`. Generated IDs use
`YYYYMMDDTHHMMSSZ-<title-slug>` with a numeric suffix on collision. New flows
start with a default phase graph: plan, plan review, implementation, review
loop, PR creation, autoreview, and merge.

Flow statuses are derived from phase and merge state. Flow statuses include
`pending`, `in_progress`, `needs_attention`, `blocked`, `completed`, `merged`,
and `abandoned`. Phase statuses include `pending`, `ready`, `running`,
`needs_attention`, `completed`, `blocked`, and `skipped`. The canonical phase
transition table, derived-readiness gate rules, and the on-disk compatibility
story live in [flow-phases.md](flow-phases.md).

The flows pane distinguishes recoverable partial states from ordinary phase
states. It shows `recover-worktree` when a saved Flow has no branch/worktree
metadata, `await-session` when a running phase has a launch attempt but no
attached provider session yet, `session-mismatch` when a phase's attached
session launch ID does not match the phase launch IDs, `missing-session-id`
when an attached session lacks a provider session ID, and `missing-pr` on a
pending Autoreview phase when PR Creation completed without structured PR
metadata.

The Plan Review phase gates Implementation. Plan Review completion must use
`--outcome approved` or `--outcome approved_with_concerns`; the latter requires
`--notes`. Use `--status needs_attention --outcome changes_requested --notes
...` for requested plan revisions, or `--status blocked --outcome blocked
--notes ...` for missing inputs or external blockers. Implementation becomes
ready only after approved Plan Review outcomes, or after an explicit
skipped-with-notes Plan Review override.

For common phase outcomes, `wtui flow phase complete`, `wtui flow phase block`,
and `wtui flow phase needs-attention` wrap the same validation and persistence
as `phase set`, then print JSON containing the updated phase, the next
actionable phase state, and allowed statuses for that next action. Notes
requirements are still enforced by the same store rules as `phase set`. Plan
Review wrappers fill the unambiguous outcomes when omitted: `complete` uses
`approved`, `block` uses `blocked`, and `needs-attention` uses
`changes_requested`. Autoreview wrappers fill `passed`, `blocked`, and
`needs_attention` for the matching common outcomes. Use
`wtui flow phase restart` to rerun a blocked or needs-attention phase as
`running`; if `--notes` is omitted, wtui records a standard rerun note. Use
`phase set` when a phase needs an explicit uncommon status or a
skipped-with-notes override.

Implementation can be split into ordered child phases with
`wtui flow phase add-child`. Child phase IDs are stable and normalized (trimmed
and lowercased): re-running the command with the same logical id -- including
case or whitespace variants -- updates the same child instead of duplicating
it, and updates collapse duplicate rows left by older records. `wtui flow phase
set` resolves phase ids the same way. Child phases currently belong
under `implementation`; they gate review loop and PR creation until completed or
skipped with notes. Flow phase launch prompts stay minimal: Plan Review and
Implementation point to the saved plan artifact, while Review Loop and PR
Creation include only the worktree, branch, and start commit metadata needed to
inspect the changes. Built-in prompts tell Plan to produce only a plan,
Implementation to use the `commit` skill, Review Loop to use the review-loop
workflow and `commit` when revisions are made, PR Creation to use the `ship`
skill, and Autoreview to use `ship` when fixes require commits or pushes.
Autoreview launch prompts include the PR target metadata but leave completion,
needs-attention, blocked, and restart mechanics to the high-level Flow phase
commands.
Override `[flow_prompts]` keys to customize those phase templates.

The PR Creation phase should record structured PR metadata with
`wtui flow pr set` after a pull request exists. The command currently supports
GitHub PRs and validates the provider, absolute http(s) URL, positive PR
number, required head/base branches, and that the head branch matches the Flow
branch. Autoreview stays pending when PR Creation is complete but this PR target
metadata is missing.

The flow state root is resolved as: `--state-root` >
`WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` >
`[sessions].root` > the user state default. In TUI startup,
`WTUI_FLOW_STATE_ROOT` has highest precedence for the shared artifact root; if
it is set, the TUI reads sessions, plans, and flows from that root.

The canonical provider-agnostic Flow skill lives at
`agent-skills/wtui-flow/`, beside `agent-skills/wtui-plan-persist/`. Install or
symlink `wtui-flow` into the user-level skill directory for supported agents
such as Codex or Claude; for Codex, a typical target is
`~/.codex/skills/wtui-flow`. The skill activates when `WTUI_FLOW_ID` and
`WTUI_FLOW_PHASE_ID` are present, reads the active flow with
`wtui flow read --flow-id "$WTUI_FLOW_ID"`, and documents the implemented
`wtui flow` / `wtui plan` commands for phase persistence and saved-plan linkage.

### `[bootstrap]`

Configures optional per-repo scripts that run after wtui successfully creates a
worktree with `n`, `P`, or `N`. Hooks are opt-in and are matched by configured
repo path. wtui does not auto-discover scripts from scanned repositories.

| Key | Type | Description |
|-----|------|-------------|
| `timeout_seconds` | integer | Default hook timeout. Omitted or `0` means `120`; negative values fail startup. |

Add one `[[bootstrap.hooks]]` entry per repo:

| Key | Type | Description |
|-----|------|-------------|
| `repo_path` | string | Required repo path to match. Supports `~` expansion. |
| `script` | string | Required script path. Relative paths resolve from the newly created worktree; `~` paths are expanded. |
| `timeout_seconds` | integer | Optional per-hook timeout override; negative values fail startup. |

Bootstrap scripts execute directly, not through a shell. The script file must
exist, be a regular file, and be executable. wtui sets the script working
directory to the new worktree and appends these environment variables:
`WTUI_REPO_PATH`, `WTUI_WORKTREE_PATH`, `WTUI_WORKTREE_REF`, and
`WTUI_WORKTREE_CREATE_KIND`.

If a hook fails, wtui keeps the created worktree and branch, refreshes the
worktree list, and shows the hook error in the status bar. For `N`, a hook
failure prevents automatic agent launch; the agent can still be launched
manually afterward.

## Agent Session Hooks

Agents launched from wtui with `a`, `N`, Flow selected-phase `enter`, or session
resume `r` are wired automatically. wtui passes Claude Code or Codex a
session-end hook that calls the current wtui binary, and it appends the
environment metadata listed below so the hook can associate the session with
the selected repo and worktree.

wtui can also ingest hook payloads from manual provider configuration:

```bash
wtui session-hook --provider claude
wtui session-hook --provider codex
```

For development and tests, pass an explicit state root:

```bash
wtui session-hook --provider codex --state-root /tmp/wtui-sessions-test
```

`session-hook` loads the normal wtui config before ingesting the hook payload.
`--state-root` overrides `[sessions].root`, and `WTUI_SESSION_STATE_ROOT`
overrides the configured root when `--state-root` is omitted. The
`copy_raw_transcripts` setting controls whether provider-native transcript data
is copied to `raw.jsonl`; it is off by default, and normalized transcript events
are still written for the sessions view.

Codex may ask you to review and trust the injected hook with `/hooks` before it
runs it. After trust is recorded for the unchanged hook command, later
wtui-launched Codex sessions can save normally.

Claude Code hook example:

```json
{
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "wtui session-hook --provider claude"
          }
        ]
      }
    ]
  }
}
```

Codex hook example:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "wtui session-hook --provider codex"
          }
        ]
      }
    ]
  }
}
```

When wtui launches or resumes a CLI agent session, it appends `WTUI_*`
environment metadata, including `WTUI_AGENT`, `WTUI_LAUNCH_ID`,
`WTUI_REPO_PATH`, `WTUI_WORKTREE_PATH`, `WTUI_BRANCH`, `WTUI_COMMIT`,
`WTUI_SESSION_STATE_ROOT`, `WTUI_PLAN_STATE_ROOT`, `WTUI_FLOW_STATE_ROOT`, and
plan or flow IDs, paths, and phase fields when available.
`codex-app` launches are the exception: wtui opens a macOS deep link, scrubs
inherited `WTUI_*` from `open`, and includes prompt-only launch metadata with
copyable `--state-root` command examples. New `codex-app` threads use the repo
path for Codex App project identity when available; the selected worktree path
is still included in the prompt metadata.

Session resume uses the stored provider session ID. Codex resumes with
`codex ... resume <session-id>` and Claude Code resumes with
`claude ... --resume <session-id>`, while preserving the same wtui hook and
metadata environment wiring as fresh launches. In the full sessions view, those
CLI resumes run inside runtime-only embedded PTYs in the sessions pane. Fresh
Flow selected-phase launches and Flow phase-session resumes run CLI agents
inside runtime-only embedded PTYs in the flows pane; Flow headless mode chooses
headless provider commands (`codex exec` / `claude --print`) versus interactive
provider commands (`codex` / `claude`) inside that embedded terminal. Other
non-Flow agent launches keep using their existing external terminal transport,
and `codex-app` Flow launches and resumes keep using deep-link transport. The
TUI refuses to resume a stored session whose provider session ID is blank (it
reports this in the status line instead), and command construction trims resume
session IDs and rejects whitespace-only ones, so a resume command never carries a blank `--resume`
argument.

Hook payloads whose `session_id` is blank or whitespace-only are rejected at
ingest time: no session record is persisted and no Flow phase attachment is
made.

For Codex hook payloads with `hook_event_name = "Stop"`, wtui records the
session as ended. Claude hook ingestion also records ended sessions, using the
payload end time when present and the current time as a fallback.

Transcripts can contain secrets, credentials, private prompts, and proprietary
code. Keep the sessions root in user-private storage and avoid committing
captured transcript files.
