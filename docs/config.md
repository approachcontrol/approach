# Approach Configuration

Approach reads an optional TOML config file before scanning repositories.

## Config Path

Approach looks for config in this order:

1. `$XDG_CONFIG_HOME/approach/config.toml`
2. `~/.config/approach/config.toml`

The file is optional. If it does not exist, Approach starts with built-in defaults.
If a config file exists but cannot be read, has malformed TOML, uses the wrong
type for a known key, or contains an invalid known value, startup fails before
repository scanning. Unknown sections and keys are ignored so config written by
a newer Approach version does not block older versions from starting. Approach only
falls through to the next path when the earlier path does not exist.

## Precedence

Environment variables remain the highest-precedence settings where they already
exist:

| Setting | Highest precedence | Config fallback | Built-in default |
|---------|--------------------|-----------------|------------------|
| Scan root | `WORKTREE_ROOT` | `[scan].root` | `~/dev` |
| Plan editor command | `[editor].command` | `EDITOR` | unset |
| Terminal command | `TERMINAL` | `[terminal].command` | platform fallback |
| Coding agent | none | `[agent].command` | unset |
| Agent model | none | `[agent].codex_model` / `[agent].claude_model` | provider default |
| Agent reasoning effort | none | `[agent].codex_reasoning_effort` / `[agent].claude_reasoning_effort` | provider default |
| Startup default view | none | `[ui].default_view` | flows view (config number `8`) |
| Plan launch prompt | none | `[agent].plan_prompt` | built-in plan implementation prompt |
| Flow phase launch prompts | none | `[flow_prompts]` | built-in Flow phase prompts |
| Flow phase graph preset | `approach flow create --preset` | `[flow].preset` | `default` |
| TUI artifact root | `APPROACH_FLOW_STATE_ROOT` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | `$XDG_STATE_HOME/approach/sessions/v1` or `~/.local/state/approach/sessions/v1` |
| Session hook root | `--state-root` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root |
| Plan state root | `--state-root` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root (`<root>/plans/...`) |
| Flow state root | `--state-root` > `APPROACH_FLOW_STATE_ROOT` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root (`<root>/flows/...`) |
| Bootstrap hook timeout | none | `[bootstrap].timeout_seconds` or hook override | `120` seconds |

`[scan].root` and `[sessions].root` support `~` and `~/...` expansion.
Session roots must resolve to absolute paths so captured transcripts stay out of
repositories. The scan root is cleaned before repository discovery; explicit
relative roots, including `WORKTREE_ROOT`, preserve relative repo paths for
compatibility. The same root is resolved from Approach's current working directory
when used as the parent directory for left-pane repo creation.

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

[ui]
default_view = 8

[agent]
command = "codex"
codex_model = "gpt-5.5"
claude_model = "claude-sonnet-5"
codex_reasoning_effort = "high"
claude_reasoning_effort = "max"
plan_prompt = "Implement the saved approach plan {title} (ID: {plan_id}) at {plan_path}. Read the plan file, then begin implementation."

[flow_prompts]
plan = "Produce a plan only for: {instructions}"
implementation = "Implement {plan_path} in {worktree_path} for issue {issue_number}, then use the commit skill before completing."
review_loop = "Use review-loop for {branch}; use commit if revisions are made."
pr_creation = "Use ship for {branch}; record PR metadata for flow {flow_id}."
autoreview = "Autoreview {pr_url}; use ship when fixes require commits or pushes."

[flow]
preset = "research"

[[flow.presets]]
name = "research"

[[flow.presets.phases]]
id = "research"
title = "Research"
kind = "plan"

[[flow.presets.phases]]
id = "draft"
title = "Draft"
kind = "implementation"
depends_on = ["research"]

[[flow.presets.phases]]
id = "publish"
title = "Publish"
kind = "merge"
depends_on = ["draft"]

[sessions]
root = "~/.local/state/approach/sessions/v1"
copy_raw_transcripts = false

[bootstrap]
timeout_seconds = 120

[[bootstrap.hooks]]
repo_path = "~/dev/approach"
script = ".approach/bootstrap"

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
| `root` | string | Directory to scan for git repositories and parent directory for repos created from the left pane. |
| `max_depth` | integer | Scan depth below `root`; `1` scans immediate children, `2` also scans one level deeper. |

When `max_depth` is omitted or set to `0`, Approach uses the scanner default of `2`.
Values greater than `2` behave like `2`.

Pressing `n` in the left repo pane creates a new local Git repository directly
under the resolved scan root, optionally creating a GitHub repo and wiring
`origin`; the creation form and repo-name rules are documented in
`docs/tui-guide.md`.

### `[ui]`

Stores user-interface preferences.

| Key | Type | Description |
|-----|------|-------------|
| `default_view` | integer | Optional startup view number. The frozen vocabulary is `1` worktrees, `2` branches, `3` stashes, `4` history, `5` reflog, `6` sessions, `7` plans, `8` flows, `9` active flows, `10` Beads Ready, `11` Beads Blocked, `12` Beads Open, `13` Beads In-Progress, and `14` Beads Closed. It deliberately differs from the grouped keyboard keys (keyboard `1` opens Git at its last-used subview, `2`–`4` open sessions/plans/flows outside Active Flows, keyboard `5` opens Beads at its last-used subview with Open as the first-entry default, `ctrl+a` toggles active flows, `6`–`9` are unbound, `w`/`b`/`s`/`h`/`r` pick Git subviews, and `r`/`b`/`o`/`i`/`c` pick Beads subviews only while Beads is active). A Git value (`1`–`5`) boots into that subview and seeds the Git group's sticky subview. A Beads value (`10`–`14`) immediately fetches that subview at startup and seeds the Beads group's sticky subview for later re-entry. Omitted keeps the built-in Flows startup default. |

Press `V` in Approach to choose and persist this value from a picker. The picker
changes future launches only; use the keyboard keys `1`–`5`, `ctrl+a`, the
scoped Git or Beads subview letters, or arrows within either group and among
the top-level Git, sessions, plans, flows, and Beads stops to switch the current
view. Arrows that enter Git or Beads then stay inside that group, so leaving it
needs a number key or `ctrl+a`.

### `[editor]`

Configures the editor used by the plans pane `e` action. The selected plan's
`plan.md` path is appended to the command, and the plans pane refreshes after
the configured command exits. Use wait flags such as `code --wait` for GUI
editors that detach by default. When this setting is omitted, Approach falls back
to `$EDITOR`.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Shell-style editor command, such as `code --wait`, `vim`, or `vim -c "set ft=markdown"`. |

### `[terminal]`

Configures the external terminal fallback used by the `t` action, detached
agent-launch scripts, and embedded-terminal detach handoff. Active tmux/Zellij
sessions still take precedence for normal `t` and agent launches, and
`TERMINAL` still overrides this setting. Embedded detach handoff is different:
after `ctrl+] d` detaches a tmux-backed embedded terminal, Approach uses
`TERMINAL`, then `[terminal].command`, then the macOS Terminal AppleScript
fallback when available. It never uses the active tmux/Zellij client, installed
inactive tmux/Zellij commands, `$SHELL`, or the current TTY as the handoff
transport.

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
command = "ghostty"
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
separate argv entries and agent launches or detach handoff append
`-e sh -c <script>`. Shell quoting is not interpreted in this setting. On macOS,
an unsupported GUI app name can open a plain worktree terminal with
`open -a <app> <path>`, but it cannot run detached agent scripts or detach
handoff. Use a supported GUI alias or a CLI terminal command for agent launches
and embedded detach handoff.

Ghostty works through the CLI command path because it accepts `-e`:
`command = "ghostty"` covers plain worktree terminals, agent launches, and
detach handoff. The `ghostty` binary must be on `PATH` (the macOS app bundle
ships it at `Ghostty.app/Contents/MacOS/ghostty`). On macOS each launch starts
a separate Ghostty app instance rather than a window in the running one.
Ghostty accepts any of its config keys as flags, so
`command = "ghostty --wait-after-command=true"` keeps windows open after a
launched agent exits instead of closing before the output can be read.

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

Stores the selected coding agent for interactive launches. Pressing `A` in Approach
opens an agent picker for `codex`, `codex-app`, or `claude` and updates this
value immediately, creating the config file if needed.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Supported values: `codex`, `codex-app`, or `claude`. |
| `codex_model` | string | Optional Codex CLI model for new launches. Supported values: `default`, `gpt-5.5`, `gpt-5.6-sol`. Empty or `default` omits the Codex override and keeps provider defaults. |
| `claude_model` | string | Optional Claude Code model for new launches. Supported values: `default`, `claude-opus-4-8`, `claude-opus-5`, `claude-sonnet-5`, `claude-fable-5`. Empty or `default` omits the Claude override and keeps provider defaults. |
| `codex_reasoning_effort` | string | Optional Codex CLI reasoning effort for new launches. Supported values: `default`, `minimal`, `low`, `medium`, `high`, `xhigh`. Empty or `default` omits the Codex override and keeps provider defaults. |
| `claude_reasoning_effort` | string | Optional Claude Code reasoning effort for new launches. Supported values: `default`, `low`, `medium`, `high`, `xhigh`, `max`. Empty or `default` omits the Claude override and keeps provider defaults. |
| `plan_prompt` | string | Optional template for the editable instructions opened by `i` in the plans pane. Supports `{title}`, `{plan_id}`, `{plan_path}`, `{repo_path}`, and `{worktree_path}`. When a saved-plan phase row is selected, it also supports `{phase_id}`, `{phase_title}`, and `{phase_status}`. Unknown placeholders remain literal. Blank or omitted uses the built-in prompt. |

Press `f2` in normal TUI views to open the prompt-template editor. The editor
can save a custom `[agent].plan_prompt`, reset it to the built-in default, or
preview the built-in prompt.

In the flows pane, `M` opens a provider-specific model picker and persists the
corresponding key for the selected CLI agent. `E` opens the reasoning-effort
picker. New Codex CLI launches use
`--model <model>` and `--config
model_reasoning_effort=<effort>`; new Claude Code launches use
`--model <model>` and `--effort <effort>`. Session resumes do not receive model
or effort flags. `codex-app` launches keep app-side/default model and reasoning
because the current deep-link path cannot carry verified provider settings.

### `[flow_prompts]`

Optional templates for Flow phase launch prompts. Blank or omitted keys use
the built-in prompt for that phase. Unknown placeholders remain literal. Approach
appends `After completing this phase goal, mark this Flow phase done with approach-flow.`
to both built-in prompts and configured templates unless the template already
ends with that exact standalone instruction.

The `f2` prompt-template editor also manages these Flow prompt keys. Saving a
blank template resets that key by removing the config override.

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
`{base_ref}`, `{issue_provider}`, `{issue_number}`, `{issue_url}`,
`{pr_provider}`, `{pr_number}`, `{pr_url}`, `{pr_head}`, `{pr_base}`, and
`{pr_status}`. Standard Plan Review, Implementation, Review
Loop, PR Creation, Autoreview, and Merge launches do not pre-read the linked
plan body, so `{plan_body}` is empty for those built-in phase types unless a
future phase path explicitly supplies it.

### `[flow]`

Configures the default phase graph for newly created Flows.

| Key | Type | Description |
|-----|------|-------------|
| `preset` | string | Optional preset name used by TUI Flow creation and `approach flow create` when `--preset` is omitted. Empty or `default` uses the built-in graph. |
| `presets` | array of tables | Custom phase graph presets. User presets cannot be named `default`, and duplicate normalized names are startup-fatal. |

Each `[[flow.presets]]` table requires `name` and one or more
`[[flow.presets.phases]]` entries:

| Phase key | Type | Description |
|-----------|------|-------------|
| `id` | string | Stable phase ID used by CLI commands and launch metadata. IDs are normalized like other Flow phase IDs. |
| `title` | string | Display title for the phase row and launch prompt. |
| `kind` | string | Optional semantic kind. Supported custom-preset kinds are `plan`, `plan_review`, `implementation`, `review_loop`, `pr_creation`, `autoreview`, and `merge`. Unknown kinds are rejected in config. |
| `depends_on` | array of strings | Optional top-level prerequisite phase IDs. Omitted or empty means the phase is a root. |

Custom presets must form an acyclic top-level graph, may include multiple root
phases, and may include at most one `merge`-kind phase. Child implementation
phases are still created dynamically with `approach flow phase add-child`; they are
not declared in config. Records created from a named preset persist
`preset_name` so Approach can restore missing `depends_on` edges if an older binary
partially rewrites the record.

### `[sessions]`

Controls portable agent-session storage. Session metadata and normalized
transcripts are stored outside repositories by default. Each provider session is
stored under a hashed session directory, with the raw provider session ID kept in
`meta.json`.

| Key | Type | Description |
|-----|------|-------------|
| `root` | string | Optional absolute state root for session files. Supports `~` expansion. |
| `copy_raw_transcripts` | boolean | Whether hook ingestion also preserves provider-native transcript JSONL as `raw.jsonl`. Defaults to `false`. |

When `root` is omitted, Approach uses `$XDG_STATE_HOME/approach/sessions/v1`, or
`~/.local/state/approach/sessions/v1` when `XDG_STATE_HOME` is unset.
Relative roots other than `~`/`~/...` fail config parsing.

`[sessions].root` doubles as the **agent-artifact root**: sessions, saved plans,
and Flow records are stored under `<root>/sessions/...`,
`<root>/plans/<plan-id>/`, and `<root>/flows/<flow-id>/`. There is no separate
plans or flows config in v1. **Moving or cleaning the sessions root therefore
also moves or removes saved plans and Flow records.**

## Saved Plans

Agents persist plans explicitly through the `approach plan` subcommands; plans are
not captured from provider hooks in v1. Each plan is stored as
`<artifact-root>/plans/<plan-id>/meta.json` plus `plan.md`, with the same
restrictive permissions (`0700` directories, `0600` files) and atomic writes as
sessions. They appear in the TUI plans pane (keyboard `3`); pane behavior is
documented in `docs/tui-guide.md`, and the edit action's editor selection in
`[editor]` above.

```bash
# Save or update (reuse --plan-id) a plan; Markdown comes from --file or stdin.
# Prints only the plan_id.
printf '%s' "$PLAN_MD" | approach plan save --title "Persist plans" [--plan-id ID] \
    [--summary TEXT] [--status STATUS] [--source SOURCE] [--provider PROVIDER] \
    [--session-id ID] [--launch-id ID] [--repo-path PATH] [--worktree-path PATH] \
    [--branch BRANCH] [--commit HASH] [--file PATH] [--state-root PATH]

approach plan phase set --plan-id ID --phase-id ID --title TITLE --status STATUS [--order N] [--state-root PATH]
approach plan list [--repo-path PATH] [--state-root PATH] --json   # --json required in v1
approach plan read --plan-id ID [--state-root PATH]                # prints Markdown only
```

Plan statuses: `draft`, `approved`, `in_progress`, `completed`, `blocked`,
`superseded`. Phase statuses: `pending`, `in_progress`, `completed`, `blocked`,
`skipped`. Phase IDs are normalized (trimmed and lowercased) before matching,
so re-running `phase set` with the same logical `--phase-id` -- including case
or whitespace variants -- updates that phase in place instead of adding a
duplicate row, and repairs records that already contain duplicates. `plan_id` must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`; when
omitted, Approach generates `YYYYMMDDTHHMMSSZ-<title-slug>` with a `-2`, `-3`, …
suffix on collision.

`save` always replaces Markdown and title from the command (both required), and
updates `status`, `source`, `summary`, and repo/session metadata only when
supplied; otherwise it preserves the stored values, `created_at`, and recorded
phases. A body-only re-save keeps the existing status.

The plan state root is resolved as: `--state-root` > `APPROACH_PLAN_STATE_ROOT` >
`APPROACH_SESSION_STATE_ROOT` > `[sessions].root` > the user state default. The
`approach plan` commands may load config to resolve the root but never scan repos or
start the TUI. Omitted metadata is filled from `APPROACH_AGENT` (provider),
`APPROACH_LAUNCH_ID`, `APPROACH_REPO_PATH`, `APPROACH_WORKTREE_PATH`, `APPROACH_BRANCH`, and
`APPROACH_COMMIT`; for new plans, and for updates that provide a repo or worktree
location, Approach also resolves best-effort repo, worktree, branch, and commit
metadata from git. `codex-app` launches do not inherit `APPROACH_*` shell environment variables
because Approach opens a macOS deep link; they receive prompt-only launch
metadata instead (see `docs/agent-sessions.md`). The `approach-plan-persist`
skill instructs agents on when and how to save plans; its canonical source
lives in `agent-skills/approach-plan-persist/` for symlinking into user-level
Codex/Claude skill directories.

## Flows

Flow records are task-centric workflow records created by the TUI or explicitly
through `approach flow`. Each record is stored as
`<artifact-root>/flows/<flow-id>/meta.json`, with restrictive permissions
(`0700` directories, `0600` files) and atomic writes. They appear in the TUI
flows pane (keyboard `4`), which is the startup default unless `[ui].default_view`
is set. The TUI can create a new Flow, launch the next launchable phase,
toggle per-Flow auto mode, resume attached phase sessions, record a manual
GitHub merge, and delete a top-level Flow record in destructive mode; pane
keys, auto-mode behavior, headless mode, model/effort pickers, and embedded
Flow terminals are documented in `docs/tui-guide.md`. Other phase and
progression mutation remains CLI/agent-driven in v1.

```bash
# Create a flow. --repo-path must be absolute, instructions are required, and
# --json is required in v1.
approach flow create --title "Ship saved plans" \
    --instructions "Plan, implement, review, open a PR, and merge." \
    --repo-path "$REPO" [--worktree-path PATH] [--branch BRANCH] \
    [--base-ref REF] [--commit HASH] [--preset NAME] [--state-root PATH] --json

# You may also read instructions from a file.
approach flow create --title "Ship saved plans" \
    --instructions-file ./instructions.md --repo-path "$REPO" --json

approach flow list [--repo-path PATH] [--state-root PATH] --json
approach flow read --flow-id ID [--state-root PATH]
approach flow phase complete --flow-id ID --phase-id ID \
    [--outcome OUTCOME] [--summary TEXT] [--notes TEXT] [--state-root PATH]
approach flow phase block --flow-id ID --phase-id ID \
    [--outcome OUTCOME] [--summary TEXT] [--notes TEXT] [--state-root PATH]
approach flow phase needs-attention --flow-id ID --phase-id ID \
    [--outcome OUTCOME] [--summary TEXT] [--notes TEXT] [--state-root PATH]
approach flow phase restart --flow-id ID --phase-id ID \
    [--notes TEXT] [--state-root PATH]
approach flow phase reset --flow-id ID --phase-id ID [--state-root PATH]
approach flow phase set --flow-id ID --phase-id ID --status STATUS \
    [--outcome OUTCOME] [--summary TEXT] [--notes TEXT] [--state-root PATH]
approach flow phase add-child --flow-id ID --parent-phase-id implementation \
    --phase-id ID --title TITLE --order N [--state-root PATH]
approach flow plan set --flow-id ID --plan-id ID [--plan-path ABSOLUTE_PATH] [--state-root PATH]
approach flow issue set --flow-id ID --provider github --number N --url URL [--state-root PATH]
approach flow pr set --flow-id ID --provider github --number N --url URL \
    --head HEAD_BRANCH --base BASE_BRANCH [--status STATUS] [--state-root PATH]
```

Transitioning a Flow phase to `completed` also syncs a linked saved-plan phase
with the same normalized phase ID; the sync and failure semantics are
documented in `docs/flow-phases.md` (Linked plan sync).

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

The flows pane layers recovery labels (`recover-worktree`, `await-session`,
`ended-session`, `session-mismatch`, `missing-session-id`, `missing-pr`) over
recoverable partial states; the labels are documented in `docs/tui-guide.md`,
and the `approach flow phase reset` recovery semantics in `docs/flow-phases.md`.

Phase gating, outcome and notes requirements, and the wrapper commands'
default outcomes are documented in `docs/flow-phases.md`. In short: the
`complete`/`block`/`needs-attention` wrappers share `phase set`'s validation
and print JSON with the updated phase and the next actionable phase state,
`restart` reruns a blocked or needs-attention phase as `running`, `reset` is
Approach-owned recovery, and `phase set` covers explicit uncommon statuses and
skipped-with-notes overrides.

Implementation can be split into ordered child phases with
`approach flow phase add-child`. Child phase IDs are stable and normalized (trimmed
and lowercased): re-running the command with the same logical id -- including
case or whitespace variants -- updates the same child instead of duplicating
it, and updates collapse duplicate rows left by older records. `approach flow phase
set` resolves phase ids the same way. Child phases currently belong
under `implementation`; they gate review loop and PR creation until completed or
skipped with notes. Flow phase launch prompts stay minimal: Plan Review and
Implementation point to the saved plan artifact, while Review Loop and PR
Creation include only the worktree, branch, and start commit metadata needed to
inspect the changes. Built-in prompts tell Plan to produce only a plan,
Plan Review to use the review-loop skill with max 6 loops, Implementation to
use the `commit` skill, Review Loop to use the review-loop workflow with goal
`review-and-revise` and `commit` when revisions are made, PR Creation to use
the `ship` skill, and Autoreview to use `ship` when fixes require commits or
pushes. All Flow phase launch prompts also end with:
`After completing this phase goal, mark this Flow phase done with approach-flow.`
Autoreview launch prompts include the PR target metadata but leave detailed
completion, needs-attention, blocked, and restart mechanics to the high-level
Flow phase commands.
Override `[flow_prompts]` keys to customize those phase templates; Approach still
appends the common phase-done instruction to custom templates.

The PR Creation phase should record structured PR metadata with
`approach flow pr set` after a pull request exists. The command currently supports
GitHub PRs and validates the provider, absolute http(s) URL, positive PR
number, required head/base branches, and that the head branch matches the Flow
branch. Autoreview stays pending when PR Creation is complete but this PR target
metadata is missing.

The Plan phase can record an optional GitHub issue reference with
`approach flow issue set`. The command stores provider, positive issue number, and
an absolute GitHub issue URL so Approach can show the Issue column and open it with
the `i` shortcut.

The flow state root is resolved as: `--state-root` >
`APPROACH_FLOW_STATE_ROOT` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` >
`[sessions].root` > the user state default. In TUI startup,
`APPROACH_FLOW_STATE_ROOT` has highest precedence for the shared artifact root; if
it is set, the TUI reads sessions, plans, and flows from that root.

The canonical provider-agnostic Flow phase skill lives at
`agent-skills/approach-flow/`, beside `agent-skills/approach-plan-persist/`. The
companion creation skill lives at `agent-skills/approach-flow-create/`. Install or
symlink both `approach-flow` and `approach-flow-create` into the user-level skill
directory for supported agents such as Codex or Claude; for Codex, typical
targets are `~/.codex/skills/approach-flow` and
`~/.codex/skills/approach-flow-create`. `approach-flow` activates when `APPROACH_FLOW_ID`
and `APPROACH_FLOW_PHASE_ID` are present, reads the active flow with
`approach flow read --flow-id "$APPROACH_FLOW_ID"`, and documents the implemented
`approach flow` / `approach plan` commands for phase persistence and saved-plan linkage.
`approach-flow-create` is for ad hoc sessions where the user asks to create a Flow
from the current task or an already-written plan. It creates the Flow, can save
and link an imported plan, and reports persistence failures explicitly. In v1,
the imported ad hoc session itself is not attached to the Flow; the created
Flow and linked plan are persisted artifacts, and future phase launches or
resumes are tracked normally.

### `[bootstrap]`

Configures optional per-repo scripts that run after Approach successfully creates a
worktree with `n`, `P`, or `N`. Hooks are opt-in and are matched by configured
repo path. Approach does not auto-discover scripts from scanned repositories.

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
exist, be a regular file, and be executable. Approach sets the script working
directory to the new worktree and appends these environment variables:
`APPROACH_REPO_PATH`, `APPROACH_WORKTREE_PATH`, `APPROACH_WORKTREE_REF`, and
`APPROACH_WORKTREE_CREATE_KIND`.

If a hook fails, Approach keeps the created worktree and branch, refreshes the
worktree list, and shows the hook error in the status bar. For `N`, a hook
failure prevents automatic agent launch; the agent can still be launched
manually afterward.

## Agent Session Hooks

Agents launched from Approach are wired with a session-end hook and `APPROACH_*`
environment metadata automatically. Manual provider hook setup, hook JSON
examples, the exported metadata, end-event handling, and resume semantics are
documented in `docs/agent-sessions.md`; embedded-terminal behavior is in
`docs/tui-guide.md`.

`session-hook` loads the normal Approach config before ingesting the hook payload.
`--state-root` overrides `[sessions].root`, and `APPROACH_SESSION_STATE_ROOT`
overrides the configured root when `--state-root` is omitted. The
`copy_raw_transcripts` setting controls whether provider-native transcript data
is copied to `raw.jsonl`; it is off by default, and normalized transcript
events are still written for the sessions view.
