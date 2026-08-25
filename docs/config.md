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
| Clipboard method | none | `[clipboard].method` | `auto` |
| OSC 52 encoded payload limit | none | `[clipboard].osc52_max_payload_bytes` | `100000` bytes |
| Desktop notifications | none | `[notifications].enabled` | `false` |
| Coding agent | none | `[agent].command` | unset |
| Agent launch backend | none | `[launch].backend` | `embedded` |
| Agent model | none | `[agent].codex_model` / `[agent].claude_model` | provider default |
| Agent reasoning effort | none | `[agent].codex_reasoning_effort` / `[agent].claude_reasoning_effort` | provider default |
| Plan launch prompt | none | `[agent].plan_prompt` | built-in plan implementation prompt |
| Flow phase launch prompts | none | `[flow_prompts]` | built-in Flow phase prompts |
| Flow phase graph preset | `approach flow create --preset` | `[flow].preset` | `default` |
| Automatic merge-phase launch | none | `[flow].auto_merge` | `false` |
| TUI artifact root | `APPROACH_FLOW_STATE_ROOT` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | `$XDG_STATE_HOME/approach/sessions/v1` or `~/.local/state/approach/sessions/v1`; a development build substitutes `approach-dev` (see [`[sessions]`](#sessions)) |
| Session hook root | `--state-root` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root |
| Plan state root | `--state-root` > `APPROACH_FLOW_STATE_ROOT` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root (`<root>/plans/...`) |
| Flow state root | `--state-root` > `APPROACH_FLOW_STATE_ROOT` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root (`<root>/approach.db`) |
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

[clipboard]
method = "auto"
osc52_max_payload_bytes = 100000

[notifications]
enabled = false

[provider]
name = "github"

[launch]
backend = "embedded"

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
auto_merge = false

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

### Legacy `[ui]` compatibility

Approach no longer has configurable startup views. Every launch starts with
repository focus, Beads/Ready stored in the top pane, Flows stored in the bottom
pane, and the top pane remembered as the first content destination. Older
`[ui].default_view` assignments remain syntactically accepted because unknown
keys are ignored, but their value has no effect. There is no startup-view picker
or persistence shortcut.

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
`iTerm.app`, `iTerm2`, `iTerm2.app`, `Ghostty`, and `Ghostty.app`. Terminal
aliases use the built-in Terminal transport. iTerm and Ghostty aliases use
AppleScript so plain worktree terminals, agent launches, and detach handoff
open a new window in the running app.

Other command values are treated as whitespace-separated CLI terminal commands
when the first field exists on `PATH`; configured arguments are preserved as
separate argv entries and agent launches or detach handoff append
`-e sh -c <script>`. Shell quoting is not interpreted in this setting. On macOS,
an unsupported GUI app name can open a plain worktree terminal with
`open -a <app> <path>`, but it cannot run detached agent scripts or detach
handoff. Use a supported GUI alias or a CLI terminal command for agent launches
and embedded detach handoff.

The `Ghostty` and `Ghostty.app` GUI aliases require Ghostty 1.3 or newer and
`osascript`. macOS asks once for Automation permission when Approach first
controls Ghostty. If Ghostty has `macos-applescript = false`, configure the
lowercase CLI form, `command = "ghostty"`, instead.

Lowercase `ghostty` stays on the CLI command path because it accepts `-e`.
This form requires the `ghostty` binary on `PATH`; the macOS app bundle ships it
at `Ghostty.app/Contents/MacOS/ghostty`. It starts a separate Ghostty app
instance on macOS rather than adding a window to the running app. Ghostty
accepts its config keys as flags, so
`command = "ghostty --wait-after-command=true"` keeps windows open after a
launched agent exits. The GUI aliases do not accept CLI arguments.

### `[clipboard]`

Controls how copy actions write to the clipboard.

| Key | Type | Description |
|-----|------|-------------|
| `method` | string | `auto` tries the platform clipboard command first and uses OSC 52 only when no supported command is installed. `system` requires a platform command. `osc52` writes directly to the controlling terminal. Values are case-insensitive and surrounding whitespace is ignored. |
| `osc52_max_payload_bytes` | integer | Maximum Base64-encoded OSC 52 payload size. The default is `100000`. `0` removes the limit. Negative values fail startup. Oversized copies fail without truncation or a partial terminal write. |

On macOS, the system method uses `pbcopy`. On Linux it tries `wl-copy`, then
`xclip`, then `xsel`. If Approach finds a native command but that command fails,
`auto` reports the failure instead of switching transports.

OSC 52 asks the terminal hosting Approach to set its clipboard. The terminal
must support OSC 52 and permit clipboard writes in its security settings. Over
SSH, that means the local hosting terminal, not the remote machine. Inside tmux,
Approach emits tmux's passthrough form. Configure tmux to permit passthrough and
clipboard writes, for example with `set -g allow-passthrough on` and
`set -g set-clipboard on`. Approach sends one complete clipboard update and
never splits or truncates the copied text.

### `[notifications]`

Controls desktop notifications emitted by the TUI.

| Key | Type | Description |
|-----|------|-------------|
| `enabled` | boolean | Emit OSC 9 notifications for observed agent exits and Flow phases that newly become `completed`, `blocked`, or `needs_attention`. The default is `false`. |

The terminal hosting Approach must support OSC 9 and allow desktop
notifications. Unsupported terminals ignore the sequence. Approach reports
only changes observed while the TUI is running, so startup does not replay old
outcomes. When Approach itself runs inside tmux, it emits tmux's passthrough
form; configure tmux with `set -g allow-passthrough on`. In tmux launch mode,
Approach checks watched windows on its periodic launch sweep. A notification
may therefore arrive up to 30 seconds after the agent exits.

### `[provider]`

Parsed for future provider-specific features.

| Key | Type | Description |
|-----|------|-------------|
| `name` | string | Future provider name, such as `github`. |

### `[launch]`

Selects where CLI agent sessions (`codex`, `claude`) run.

| Key | Type | Description |
|-----|------|-------------|
| `backend` | string | `embedded` (default) or `tmux`. `tmux` opts into tmux mode: most interactive CLI agent launches run as windows in a per-repo tmux session on your default tmux server instead of in the embedded terminal. Any other value is a startup error. |
| `prefer_multiplexer` | boolean | Superseded by `backend`. Still parsed so existing config files keep loading; it has no effect. |

#### tmux mode

With `backend = "tmux"`:

- Each repo gets one tmux session named `approach-<repo-dir>-<hash>` on your
  default tmux server, created on the first launch and reused after that. Each
  launch is a new window in it, named after the phase or agent. Dots in the
  directory name become dashes, because tmux reads a dot in a target name as a
  pane separator.
- Sessions live on the default server, so `tmux ls` and `tmux attach` from your
  own terminal see them, and they survive Approach quitting. Quitting Approach
  never terminates them.
- The window closes when the agent exits; the session ends with its last
  window and is recreated by the next launch.
- The first launch for a repo also opens a terminal window attached to that
  session, using `$TERMINAL`, `[terminal].command`, or the macOS Terminal
  fallback — the same resolution the `T` key uses. Later launches add a window
  to the session that terminal is already watching. When no terminal resolves,
  the launch still succeeds and the status line falls back to naming the
  `tmux attach -t <session>` command. Approach running inside tmux or Zellij
  opens no window at all.
- If `tmux` is not on `PATH`, launches that would have taken the tmux route fall
  back to their default-backend behavior and say so in the status line. The
  availability check itself never refuses a launch, but a tmux launch that fails
  to spawn is reported as a launch failure rather than falling back.
- Headless launches (Flow-level `headless`, including every AutoMode launch,
  which is always headless) stay in the embedded terminal even in tmux mode:
  `claude --print` buffers all output until it exits, so a self-closing tmux
  window would show nothing. They take the route silently — the fallback note is
  only for launches that wanted tmux and could not have it.
- Flow repair and the plan launch Flow creation performs stay embedded.

`docs/tui-guide.md` lists exactly which launches move, what stays put, the
attach key, and the limitations tmux mode's ownership model carries.

### `[agent]`

Stores the selected coding agent for interactive launches. Pressing `A` in Approach
opens an agent picker for `codex`, `claude`, or `cursor-agent` and updates this
value immediately, creating the config file if needed.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Supported values: `codex`, `claude`, or `cursor-agent`. |
| `codex_model` | string | Optional Codex CLI model for new launches. Supported values: `default`, `gpt-5.5`, `gpt-5.6-sol`. Empty or `default` omits the Codex override and keeps provider defaults. |
| `claude_model` | string | Optional Claude Code model for new launches. Supported values: `default`, `claude-opus-4-8`, `claude-opus-5`, `claude-sonnet-5`, `claude-fable-5`. Empty or `default` omits the Claude override and keeps provider defaults. |
| `cursor_model` | string | Optional Cursor CLI model for new launches. Supported values: `default`, `composer-2.5`, `grok-4.6`, `opus-5`, `fable-5`, `gpt-5.6-sol`. Empty or `default` omits `--model` so Cursor uses the account CLI default. `composer-2.5` is the current Composer SKU; Cursor has no floating `composer` alias that tracks later Composer releases. |
| `codex_reasoning_effort` | string | Optional Codex CLI reasoning effort for new launches. Supported values: `default`, `minimal`, `low`, `medium`, `high`, `xhigh`. Empty or `default` omits the Codex override and keeps provider defaults. |
| `claude_reasoning_effort` | string | Optional Claude Code reasoning effort for new launches. Supported values: `default`, `low`, `medium`, `high`, `xhigh`, `max`. Empty or `default` omits the Claude override and keeps provider defaults. |
| `plan_prompt` | string | Optional template for the editable instructions opened by `i` in the plans pane. Supports `{title}`, `{plan_id}`, `{plan_path}`, `{repo_path}`, and `{worktree_path}`. When a saved-plan phase row is selected, it also supports `{phase_id}`, `{phase_title}`, and `{phase_status}`. Unknown placeholders remain literal. Blank or omitted uses the built-in prompt. |

Press `f2` in normal TUI views to open the prompt-template picker. From it you
can edit `[agent].plan_prompt` (`enter`, then `ctrl+s` or enhanced
`ctrl+enter` to save; `enter` inserts a newline), reset it to the built-in
default (`r`, then confirm), or preview the
built-in prompt (`v`). See `docs/tui-guide.md` for the full key map.

In the flows pane, `M` opens a provider-specific model picker and persists the
corresponding key for the selected CLI agent. `E` opens the reasoning-effort
picker. New Codex CLI launches use
`--model <model>` and `--config
model_reasoning_effort=<effort>`; new Claude Code launches use
`--model <model>` and `--effort <effort>`; new Cursor CLI launches use
`--model <model>` and have no reasoning-effort flag. Session resumes do not receive model
or effort flags.

These values are also stamped onto every phase of a Flow when it is created, so
each Flow records the agent, model, and reasoning effort that were in effect at
creation time; see `docs/flow-phases.md`. Nothing consumes the stamped values at
launch yet.

### `[flow_prompts]`

Optional templates for Flow phase launch prompts and the `U` autofix launcher.
Blank or omitted keys use the built-in prompt for that key. Unknown placeholders
remain literal. Approach appends a two-line suffix to both built-in phase
prompts and configured phase templates: `Before your final response, wait for
every spawned background or delegated task to finish and consume its result;
if any cannot finish safely, stop it and persist needs_attention or blocked
with useful notes.` followed by `After completing this phase goal, mark this
Flow phase done with approach-flow.` A template already ending with that exact
pair is left alone; a template ending with only the phase-done instruction has
that line replaced with the full pair. The `autofix` key is not a phase prompt
and never receives that suffix.

The `f2` prompt-template picker also manages these Flow prompt keys. Saving a
blank or whitespace-only template with `ctrl+s`, or with `ctrl+enter` in a
terminal that reports the distinct chord, resets that key by removing the
config override, the same as a confirmed `r` from the picker.

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
| `autofix` | string | Template for the `U` autofix launcher. This is not a phase prompt and does not receive the phase-done instruction suffix. Blank or omitted uses `autofix pr #{pr_number}`. Headless and tmux launches send the rendered prompt on argv; interactive embedded launches prefill the dock with it. |

Supported Flow placeholders are `{approach_bin}`, `{flow_id}`, `{flow_title}`,
`{instructions}`, `{phase_id}`, `{phase_title}`, `{plan_id}`, `{plan_path}`,
`{plan_body}`, `{repo_path}`, `{worktree_path}`, `{branch}`, `{commit}`,
`{base_ref}`, `{issue_provider}`, `{issue_number}`, `{issue_url}`,
`{pr_provider}`, `{pr_number}`, `{pr_url}`, `{pr_head}`, `{pr_base}`, and
`{pr_status}`. Standard Plan Review, Implementation, Review
Loop, PR Creation, Autoreview, and Merge launches do not pre-read the linked
plan body, so `{plan_body}` is empty for those built-in phase types unless a
future phase path explicitly supplies it.

Use `{approach_bin}` for every `approach` invocation a template tells the agent
to run. It expands to the shell-quoted path of the binary that launched the
agent, which is what the built-in prompts use; a literal `approach` resolves
from the agent's ambient `PATH` instead and can be a different build from the
one that owns the Flow database. On a launch that carries no pin it expands to
the same self-resolving `"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}"`
form documented by the unified skill, so a template written this way works
either way.

### `[flow]`

Configures Flow defaults and the global automatic merge-phase policy.

| Key | Type | Description |
|-----|------|-------------|
| `preset` | string | Optional preset name used by TUI Flow creation and `approach flow create` when `--preset` is omitted. Empty or `default` uses the built-in graph. |
| `auto_merge` | boolean | Launch a ready semantic merge phase headlessly when the Flow has no explicit override. Missing values are `false`. `ctrl+g` persists this value before changing the running policy. |
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
`preset_name` so the one-time legacy migrator can restore missing `depends_on`
edges before SQLite becomes authoritative.

### `[sessions]`

Controls portable agent-session storage. Session metadata and normalized
transcripts are stored outside repositories by default. Each provider session is
stored under a hashed session directory, with the raw provider session ID kept in
`meta.json`.

| Key | Type | Description |
|-----|------|-------------|
| `root` | string | Optional absolute state root for session files. Supports `~` expansion. |
| `copy_raw_transcripts` | boolean | Whether hook ingestion also preserves provider-native transcript JSONL as `raw.jsonl`. Defaults to `false`. |

When `root` is omitted, a released build uses `$XDG_STATE_HOME/approach/sessions/v1`,
or `~/.local/state/approach/sessions/v1` when `XDG_STATE_HOME` is unset. A build
whose version is not a published release tag (`make build`, `go run`, a snapshot)
substitutes `approach-dev` for `approach` in that path, so development state
never shares a database with the release you have installed. Setting `root`
explicitly opts out of that split — both builds then use exactly what you named.
Relative roots other than `~`/`~/...` fail config parsing.

`[sessions].root` doubles as the **agent-artifact root**: sessions, saved plans,
and Flow records are stored under `<root>/sessions/...`,
`<root>/plans/<plan-id>/`, and `<root>/approach.db`. There is no separate
plans or flows config in v1. **Moving or cleaning the sessions root therefore
also moves or removes saved plans and Flow records.**

Three more files live beside the database, all created by `approach db migrate`
or a TUI start and none of them configurable in v1:
`<root>/approach.db.meta.json` records which build migrated the database and
when, `<root>/backups/` holds the verified pre-migration copies (8 per migrated
file; `approach db migrate --backup-dir PATH` writes them elsewhere), and
`<root>/.approach.db.bootstrap.lock` is the migration lease.

**Migration is not automatic.** After a release that bumps the flow database
schema, `approach flow`, `approach serve`, and the session hook refuse the root
and name `approach db migrate`; run that, or start the TUI. `approach db inspect
[--json]` reports what is in a root without opening a store, taking that lock, or
repairing anything, so it still answers while a migration is running.

One consequence for a root whose permissions are looser than `0700`: `approach
flow list` and `approach serve` used to tighten it as a side effect of opening
it. They no longer do — they report the mode and warn, because repairing it
would erase the state `approach db inspect` exists to show. A first `approach
serve` against a `0755` root therefore leaves a `0600` database inside a `0755`
directory. Mutating commands and the TUI still tighten the root to `0700`.

## Saved Plans

Agents persist plans explicitly through the `approach plan` subcommands; plans are
not captured from provider hooks in v1. Each plan is stored as
`<artifact-root>/plans/<plan-id>/meta.json` plus `plan.md`, with the same
restrictive permissions (`0700` directories, `0600` files) and atomic writes as
sessions. They appear in the TUI plans pane (bottom-pane keyboard `2`); pane behavior is
documented in `docs/tui-guide.md`, and the edit action's editor selection in
`[editor]` above.

```bash
# Save or update (reuse --plan-id) a plan; Markdown comes from --file or stdin.
# Prints only the plan_id unless --json is supplied.
printf '%s' "$PLAN_MD" | approach plan save --title "Persist plans" [--plan-id ID] \
    [--summary TEXT] [--status STATUS] [--source SOURCE] [--provider PROVIDER] \
    [--session-id ID] [--launch-id ID] [--repo-path PATH] [--worktree-path PATH] \
    [--branch BRANCH] [--commit HASH] [--file PATH] [--json] [--state-root PATH]

approach plan status set --plan-id ID --status STATUS [--state-root PATH]
approach plan phase set --plan-id ID --phase-id ID --title TITLE --status STATUS [--order N] [--state-root PATH]
approach plan list [--repo-path PATH] [--state-root PATH] --json   # --json required in v1
approach plan read --plan-id ID [--json] [--state-root PATH]
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

The plan state root is resolved as: `--state-root` > `APPROACH_FLOW_STATE_ROOT` >
`APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` > `[sessions].root` > the user state default. The
`approach plan` commands may load config to resolve the root but never scan repos or
start the TUI. Omitted metadata is filled from `APPROACH_AGENT` (provider),
`APPROACH_LAUNCH_ID`, `APPROACH_REPO_PATH`, `APPROACH_WORKTREE_PATH`, `APPROACH_BRANCH`, and
`APPROACH_COMMIT`; for new plans, and for updates that provide a repo or worktree
location, Approach also resolves best-effort repo, worktree, branch, and commit
metadata from git. The unified `approach-flow` skill instructs agents on when
and how to save plans; its canonical source lives in
`agent-skills/approach-flow/` for symlinking into user-level Codex/Claude skill
directories.

## Flows

Flow records are task-centric workflow records created by the TUI or explicitly
through `approach flow`. Records are stored in `<artifact-root>/approach.db`, a
`0600` pure-Go SQLite database inside the `0700` artifact root. Runtime uses WAL
and a five-second busy timeout by default; writers serialize database-wide while
WAL readers continue. They appear in the TUI
flows pane (bottom-pane keyboard `3`), which is stored at startup below
Beads/Ready. The TUI can create a new Flow, launch the next launchable phase,
toggle per-Flow auto mode and the per-Flow headless preference, resume attached phase sessions, record a manual
GitHub merge, and delete a top-level Flow record in destructive mode; pane
keys, auto-mode behavior, headless mode, model/effort pickers, and embedded
Flow terminals are documented in `docs/tui-guide.md`. Other phase mutation
remains CLI/agent-driven in v1.

`FlowRecord.Bead` is independent of `FlowRecord.Issue`. `Bead` stores the Beads
ID and, when the selected issue is a child with a known parent, its epic ID;
`Issue` remains the optional GitHub issue link managed by `approach flow issue
set`. Ready-Beads `f` and `F` populate `Bead` from the already-loaded selected
row without invoking or mutating `bd`. Manual TUI/CLI Flow creation remains
unlinked. JSON writes `"bead":{"id":"child","epic_id":"epic"}` for a
linked child, omits `epic_id` when unknown, and omits `bead` entirely for an
unlinked Flow. An epic ID without a Bead ID is rejected.

On first open when `approach.db` is absent and legacy `<artifact-root>/flows/`
records exist, Approach canonicalizes the readable legacy corpus into a closed
staged database, validates it, and atomically promotes the database. `flows/`
is **left exactly where it is** and a notice naming it is reported (see below).
Moving it is invisible to this build but silently empties the Flow list of any
build still on the file store, including one launched by accident against a
shared state root, so both representations are allowed to coexist until every
build in use is on the SQLite backend. Remove `flows/` by hand once that is
true. Interrupted cutovers resume from the staged database, which is validated
for schema and integrity only — the resume deliberately does not re-derive
records from the legacy source, so editing presets after an interrupted cutover
cannot wedge it; if that stage is unusable and `flows/` is still present, the
stage is discarded and rebuilt from source instead. Once `approach.db` exists,
it is authoritative and no legacy directory affects runtime reads. Saved plans
and agent sessions remain file-backed.

The cutover writes what it did to stderr **and** to
`<artifact-root>/FLOW-MIGRATION-NOTICE.txt`, because the TUI takes the alternate
screen immediately after startup and the stderr copy is easy to miss. The notice
reports how many of the records found were migrated, names anything skipped, and
names any Flow whose phase graph could not be rebuilt. It is advisory; delete it
once read.

Migration failure modes:

- A legacy `meta.json` that cannot be read (permissions, I/O errors) **aborts
  the migration** and names the offending Flow. Nothing is moved or promoted,
  so `flows/` stays authoritative; fix the cause and start Approach again.
- A legacy `meta.json` that cannot be decoded (corrupt JSON, a mismatched
  `flow_id`, an unsupported `schema_version`) is skipped and named in the
  notice. Such a record was already invisible to the file-backed store, so
  nothing that used to work stops working, and the original bytes stay under
  `flows/`. Note that repairing it no longer brings the Flow back on the next
  launch: `flows/` is not read again once `approach.db` exists. Recovering it
  means repairing the file and re-running the cutover, which is only safe
  immediately after the migration — see the rollback warning below.
- A Flow that names a preset missing from your config keeps its
  `missing_edges_unresolved` marker, which blocks phase mutation on it. Preset
  edge recovery runs **only** during migration, so restoring the preset later
  does not heal the record on its own. The notice names these Flows, and
  **immediately after the migration** — while `approach.db` holds nothing but
  what was just imported — you can restore the presets, move `approach.db`
  aside, and restart to re-run the cutover from `flows/`. Do not do this later:
  `flows/` is a snapshot frozen at migration time, not a live mirror, so
  re-running the cutover once you have created or changed Flows discards all of
  them. After that point the remedies are to recreate the Flow, or to repair it
  directly in `approach.db` — the `flows` table stores each record as JSON in
  its `record` column, and the repair takes **two** edits: set `depends_on` on
  the phases *and* delete the `graph_recovery` object holding
  `missing_edges_unresolved`. The fence keys off that marker, not off the edges,
  so setting `depends_on` alone leaves the Flow blocked.
- If the cutover is interrupted, the next launch resumes from the staged
  database when it validates. When it does not, and `flows/` is still present
  (or there is nothing to migrate at all), the stage is discarded and rebuilt —
  an interrupted first bootstrap never blocks startup. `flows/` is never moved
  or modified by Approach, so it always remains a complete copy of the records
  as they stood on migration day — but see the rollback warning below before
  treating that as a general undo.
- **A resumed cutover promotes the stage exactly as it was built.** It is
  deliberately not re-derived from `flows/`, so any Flow *added to* or *changed
  in* `flows/` between the interrupted run and the resume — which happens if you
  fall back to a pre-SQLite build in between — is not in the database. The
  notice names the additions it can detect; changes to already-staged records
  keep their pre-change form and cannot be detected by ID. Re-running the
  cutover picks them up, subject to the rollback warning below.
- If `approach.db` fails validation, the error names `flows/` when it is still
  there: move `approach.db` aside — keep it, it holds anything created since the
  migration and may be repairable by hand — and restart to re-migrate from the
  originals. Note that this check covers schema *shape* as well as readability,
  so a database whose data is perfectly intact can land here; the rollback
  warning below applies in full. A database written by a *newer* Approach is
  reported separately and carries no such advice: it is not damaged, and the
  only correct action is to upgrade.
- A root migrated by an earlier build that did rename `flows/` still has a
  `flows.legacy/` directory. It is a copy of the original and is never read once
  `approach.db` exists; keep or remove it as you like.

> **Re-running the cutover is not an undo.** Approach never writes to `flows/`
> after the migration, so it stays frozen at migration-day state. Moving
> `approach.db` aside and restarting rebuilds the database from that snapshot,
> which silently discards every Flow created or changed since. It is the right
> move in the minutes after a migration, while the database holds nothing else —
> and the wrong move at any later point. The migration notice says so where it
> offers it; nothing else in Approach will ever suggest it.

The database and its records have separate compatibility gates:

- `PRAGMA user_version` covers the database as a whole. Version 2 added the
  `flows.bead_id` and `flows.epic_id` non-null text projections, both defaulting
  to the empty string. Version 3 adds `flows.prepared_at TEXT NOT NULL DEFAULT
  ''`, the exact protected-receipt trigger, and
  `epic_progressions(repo_path, epic_id, enabled, updated_at, record)` with a
  composite primary key and no enabled-scan index yet. Version 4 adds
  required boolean `done` inside each progression record and installs insert
  and record-update triggers that reject missing, null, or non-boolean `done`.
  Version 5 leaves those columns unchanged and installs the progression-claim
  marker compatibility trigger. Version 6 adds
  `flows.preparation_nonce TEXT NOT NULL DEFAULT ''` plus a compatibility
  trigger that keeps the projection and storage-only JSON field identical.
  Version 7 adds `flows.recovery_generation INTEGER NOT NULL DEFAULT 0` and a
  compatibility trigger for persisted phase reconciliation and
  recovered-launch capability fields.
  Under the bootstrap
  lease, existing version 0
  (unstamped v1 layout) and version 1 databases are validated against the exact
  predecessor table-and-index contract; version 2 is validated against its
  exact columns, indexes, and Bead trigger; version 3 is validated against its
  full schema before any record changes; version 4 is validated against the
  parent-release contract (done triggers, no claim or nonce trigger); version 5
  is validated against the claim-marker trigger without a nonce projection;
  version 6 is validated against the nonce projection and trigger. All upgrade
  transactionally in place to version 7. The v3→v4 step strictly decodes
  every legacy progression blob and rewrites it with `done:false`, preserving
  identity, enabled/halt state, timestamps, and SQL projections exactly. The
  v4→v5 step installs only the claim-marker trigger. The v5→v6 step adds the
  nonce projection and trigger. The v6→v7 step adds the recovery-generation
  projection and trigger without rewriting Flow blobs. Historical disabled rows
  are conservative normal-off rows because their cause cannot be recovered.
  Existing Flow JSON blobs, earlier projections, and retained `flows/` files
  are not rewritten or removed. A malformed predecessor progression aborts and
  rolls back every rewrite, new trigger, and the version stamp. Other malformed
  predecessors are rejected
  before any column, table, trigger, or version stamp changes. Version 2 also installed an
  exact compatibility trigger: if an Approach process that was already running
  before the upgrade later tries to rewrite a linked Flow using the predecessor
  JSON shape, SQLite rejects the write instead of letting it strip `bead` while
  retaining the physical projections. Restart that older process with the
  upgraded build before retrying its write. Version 3's receipt trigger likewise
  rejects an older rewrite that removes or changes a prepared Flow's JSON
  receipt while its projection remains protected. Version 5's claim-marker
  trigger likewise rejects an older rewrite that removes a persisted
  progression-claim marker. Version 6's nonce trigger also rejects a
  predecessor process that was already open when migration ran and then tries
  to erase a receipt-less preparation's exact generation token. Version 7's
  trigger rejects an already-open version 6 writer that attempts to rewrite a
  Flow containing reconciliation or recovered-launch state without advancing
  the new generation projection. It also makes every version 6 build refuse
  the database on a fresh open. A value newer than this build
  supports prevents the store from opening and reports that Approach must be
  upgraded. This is not corruption and is never downgraded to a partial result.
- Each JSON record carries its own `schema_version`. A malformed record or a
  record written with a newer per-record version does not prevent healthy rows
  from being listed. List operations skip only rows that SQLite scanned
  successfully but Approach could not decode, preserve the normal
  `updated_at DESC, flow_id ASC` order for healthy rows, and report every
  skipped Flow ID. Query, scan, iteration, and close failures remain fatal.

Progression records use their own codec v1. The key is a canonical absolute
repository path plus trimmed epic ID; absence means normal disabled, while a
malformed row is an error. `done` is a required JSON boolean and the
authoritative completion signal; disabled is never interpreted as done. Valid
states are active `(enabled, !done, no halt)`, normal off `(disabled, !done, no
halt)`, successfully exhausted `(disabled, done, no halt)`, and halted
`(disabled, !done, complete child/status/message tuple)`. Enabled+done,
done+halt, and enabled+halt are invalid. Only active may newly become done;
repeating done and every complete-state no-op preserve timestamps. Explicit
manual off clears done while retaining a sticky halt. Ordinary and prepared
enable clear both done and halt.

Point reads stay strict: `approach flow read --flow-id ID` returns the damaged
row's decode, version, or physical-projection mismatch and never reports it as
not found. Every read compares `bead_id` and `epic_id` with the decoded JSON in
the same way as the existing identity/status projections. By contrast,
`approach flow list --json` writes the healthy subset as a valid JSON array to
stdout, writes the skipped-row diagnostic to stderr, and exits successfully.
`--repo-path` scopes both the returned rows and the diagnostic. If every
matching row is unreadable, stdout is still `[]` and stderr carries the
diagnostic. Any non-partial storage failure exits unsuccessfully instead.

The Repository Flows and Active Flows panes show skipped rows as a persistent
degraded-data warning, separate from the existing cached-data refresh warning;
the banner remains visible even when no healthy rows remain. Repository and
Active Flows own independent warnings. AutoMode never launches from a partial
corpus and retains its last complete comparison snapshot until a clean list
succeeds.

Approach deliberately does not quarantine, delete, repair, or rewrite an
unreadable authoritative row automatically. It also does not roll back the
database or re-migrate from the frozen legacy `flows/` snapshot: either action
could discard newer data or destroy a record that only a newer build can read.
Inspect the stderr/server log diagnostic, upgrade first when a version is newer,
and make any manual recovery decision against a preserved copy of
`approach.db`.

```bash
# Create a Flow record. --repo-path must be absolute, instructions are required,
# and --json is required in v1.
approach flow create --title "Ship saved plans" \
    --instructions "Plan, implement, review, open a PR, and merge." \
    --repo-path "$REPO" [--worktree-path PATH] [--branch BRANCH] \
    [--base-ref REF] [--commit HASH] [--preset NAME] [--state-root PATH] --json

# Create and prepare a dedicated Flow worktree through the same lifecycle as the
# TUI. When --repo-path is omitted, the current checkout's main worktree is used.
approach flow create --title "Ship saved plans" \
    --instructions-file ./instructions.md --prepare-worktree \
    [--repo-path "$REPO"] [--base-ref REF] [--preset NAME] [--state-root PATH] --json

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
approach flow plan save --flow-id ID [--plan-id ID] [--title TITLE] \
    [--status STATUS] [--summary TEXT] [--file PATH] [--state-root PATH]
approach flow plan set --flow-id ID --plan-id ID [--plan-path ABSOLUTE_PATH] [--state-root PATH]
approach flow issue set --flow-id ID --provider github --number N --url URL [--state-root PATH]
approach flow pr set --flow-id ID --provider github --number N --url URL \
    --head HEAD_BRANCH --base BASE_BRANCH [--status STATUS] [--state-root PATH]
```

Inside an Approach launch, Flow commands default `--flow-id` and `--phase-id`
from `APPROACH_FLOW_ID` and `APPROACH_FLOW_PHASE_ID`; plan commands default
`--plan-id` from `APPROACH_PLAN_ID`, and plan phase updates prefer
`APPROACH_PLAN_PHASE_ID` before falling back to the Flow phase. The explicit
flags above remain available for ad hoc commands and cross-Flow updates.
When `flow plan save` explicitly targets a Flow other than the launched one,
an omitted `--plan-id` uses that target Flow's linked plan rather than
`APPROACH_PLAN_ID`; pass `--plan-id` to override it deliberately.
`approach flow plan save` reads Markdown from `--file` or stdin, saves and
reads back the plan, seeds any missing top-level implementation phases without
regressing existing progress, links the plan to the Flow, and prints JSON. Its
Flow read and Flow link both go through the launch controller when
`APPROACH_CONTROL_ENDPOINT` names one, exactly as the `flow read` and
`flow plan set` it is shorthand for do, so the composite stays available to a
pinned agent whose own build can no longer open a migrated state root; a link
that can only be spooled prints `linked: false` and exits 0, with the plan
itself already persisted. It
does not complete the Flow's Plan phase; phase progression remains an explicit
`approach flow phase ...` operation. If the plan persists but the later Flow
link fails, the command's error names the saved plan ID and path so the caller
can inspect or link that partial result safely.

Transitioning a Flow phase to `completed` also syncs a linked saved-plan phase
with the same normalized phase ID; the sync and failure semantics are
documented in `docs/flow-phases.md` (Linked plan sync).

Flow IDs use the same safe single-path-segment shape as plans:
`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`. Generated IDs use
`YYYYMMDDTHHMMSSZ-<title-slug>` with a numeric suffix on collision. New flows
start with a default phase graph: plan, plan review, implementation, review
loop, PR creation, autoreview, and merge. New TUI, Ready-Bead, and CLI-created
Flows also persist `headless: true` unless the New Flow form explicitly seeds
`false`. Legacy records without `headless` read as `true`, while a stored
`false` is preserved across reads, lists, and later metadata writes.

Flow statuses are derived from phase and merge state, except `closed`, which
records an explicit close and outranks every other status. Flow statuses include
`pending`, `in_progress`, `needs_attention`, `blocked`, `completed`, `merged`,
`abandoned`, and `closed`. Phase statuses include `pending`, `ready`, `running`,
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
`Before your final response, wait for every spawned background or delegated
task to finish and consume its result; if any cannot finish safely, stop it
and persist needs_attention or blocked with useful notes.` followed by
`After completing this phase goal, mark this Flow phase done with approach-flow.`
Autoreview launch prompts include the PR target metadata but leave detailed
completion, needs-attention, blocked, and restart mechanics to the high-level
Flow phase commands.
Override `[flow_prompts]` keys to customize those phase templates; Approach still
appends the common pending-work and phase-done instructions to custom templates.

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

The canonical provider-agnostic skill lives at `agent-skills/approach-flow/`.
It routes active phase work, ad hoc Flow creation, and standalone plan
persistence to focused references in the same package. Install or symlink the
single `approach-flow` skill into the user-level skill directory for supported
agents such as Codex or Claude; for Codex, a typical target is
`~/.codex/skills/approach-flow`, though Codex also reads the shared
`~/.agents/skills` directory. Symlink `agent-skills/approach-flow/` into the
chosen directory to track the checkout, or copy it for a detached installation.
`approach-flow` activates for launched phase/plan context and for ad hoc
requests to create a Flow or persist a plan. Launch-aware CLI defaults let the
skill use `approach flow read`, `approach flow plan save`, and plan lifecycle
commands without repeating IDs or root flags. In v1, an imported ad hoc session
itself is not attached to the Flow; the created Flow and linked plan are
persisted artifacts, and future phase launches or resumes are tracked normally.

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
