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
| Agent launch backend | none | `[launch].backend` | `embedded` |
| Agent model | none | `[agent].codex_model` / `[agent].claude_model` | provider default |
| Agent reasoning effort | none | `[agent].codex_reasoning_effort` / `[agent].claude_reasoning_effort` | provider default |
| Plan launch prompt | none | `[agent].plan_prompt` | built-in plan implementation prompt |
| Flow phase launch prompts | none | `[flow_prompts]` | built-in Flow phase prompts |
| Flow phase graph preset | `approach flow create --preset` | `[flow].preset` | `default` |
| TUI artifact root | `APPROACH_FLOW_STATE_ROOT` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | `$XDG_STATE_HOME/approach/sessions/v1` or `~/.local/state/approach/sessions/v1`; a development build substitutes `approach-dev` (see [`[sessions]`](#sessions)) |
| Session hook root | `--state-root` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root |
| Plan state root | `--state-root` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root (`<root>/plans/...`) |
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
opens an agent picker for `codex` or `claude` and updates this
value immediately, creating the config file if needed.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Supported values: `codex` or `claude`. |
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
or effort flags.

These values are also stamped onto every phase of a Flow when it is created, so
each Flow records the agent, model, and reasoning effort that were in effect at
creation time; see `docs/flow-phases.md`. Nothing consumes the stamped values at
launch yet.

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
metadata from git. The `approach-plan-persist`
skill instructs agents on when and how to save plans; its canonical source
lives in `agent-skills/approach-plan-persist/` for symlinking into user-level
Codex/Claude skill directories.

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
  Under the bootstrap
  lease, existing version 0
  (unstamped v1 layout) and version 1 databases are validated against the exact
  predecessor table-and-index contract; version 2 is validated against its
  exact columns, indexes, and Bead trigger; version 3 is validated against its
  full schema before any record changes; version 4 is validated against the
  parent-release contract (done triggers, no claim or nonce trigger); version 5
  is validated against the claim-marker trigger without a nonce projection. All
  upgrade transactionally in place
  to version 6. The v3→v4 step strictly decodes
  every legacy progression blob and rewrites it with `done:false`, preserving
  identity, enabled/halt state, timestamps, and SQL projections exactly. The
  v4→v5 step installs only the claim-marker trigger. The v5→v6 step adds the
  nonce projection and trigger. Historical disabled rows
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
  to erase a receipt-less preparation's exact generation token. A value newer than this build
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
`~/.codex/skills/approach-flow-create`, though Codex also reads the shared
`~/.agents/skills` directory. `agent-skills/install.sh` performs that
installation for all three skills, defaulting to whichever of
`~/.claude/skills` and `~/.agents/skills` has an existing agent home; use
`--target DIR` for any other location such as `~/.codex/skills`. It also
supports `--copy`, `--dry-run`, and `--force`. It replaces stale symlinks — including ones left dangling by a moved
checkout — but skips real directories unless `--force` is given.
`approach-flow` activates when `APPROACH_FLOW_ID`
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
