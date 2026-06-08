# AGENTS.md

## Commands

```bash
make build          # build bin/wtui
make test           # run all tests
make run            # build and run the TUI
go test ./scanner   # run one package
gofmt -l .          # formatting check used by CI
```

## Project Shape

`wtui` is a Go Bubble Tea TUI for managing git worktrees across repositories.

- `cmd/wtui/main.go` parses `--version`/`-v`, handles `session-hook --provider claude|codex`, the `plan save|list|read|phase set` subcommands, and the `flow create|list|read|phase set|phase add-child|plan set` subcommands, loads config, applies `WORKTREE_ROOT`, and wires `scanner.Scan()` into the Bubble Tea program. Plan and Flow subcommands live in `cmd/wtui/plan.go` and `cmd/wtui/flow.go`; they resolve the artifact root without scanning repos or starting the TUI.
- `config/` loads optional TOML from `$XDG_CONFIG_HOME/wtui/config.toml` or `~/.config/wtui/config.toml`; missing config is non-fatal, unreadable or malformed existing config is startup-fatal, and parsed fields include scan, editor, terminal, provider, launch, agent, sessions, and bootstrap settings.
- `scanner/` discovers repos under `WORKTREE_ROOT`, `[scan].root`, or `~/dev` (default up to 2 levels deep, configurable down with `[scan].max_depth`), excluding `*-worktrees`.
- `gitquery/` shells out to git for worktrees, branches, stashes, history, reflog, and diffs; `parse.go` holds the pure parsing split out from execution, and `runner.go` defines the `Runner` seam (the git CLI adapter) wrapped by a `Querier`. Package-level functions delegate to a default `Querier`; `NewQuerier(runner)` injects a fake `Runner` for tests.
- `actions/` performs git mutations (create/remove/prune/unlock worktree, delete branch, drop stash, fetch `--prune`, pull `--ff-only`) plus clipboard, VS Code, tmux/Zellij terminal launching, and Codex/Claude launch/resume command construction with wtui hook metadata.
- `sessions/` stores portable agent-session metadata and normalized transcripts under the user state directory, ingests Claude/Codex hook payloads, resolves git metadata, and exposes `Store` methods for list/read/finalize behavior. Session directories are keyed by a hash of provider session ID rather than the raw ID, and store roots must be absolute.
- `planstore/` persists saved plans beside sessions under the shared agent-artifact root (`<root>/plans/<plan-id>/meta.json` + `plan.md`, dirs `0700`/files `0600`, atomic writes). `Store` exposes `Save` (validates title/content/status, generates timestamp+slug IDs with collision suffixes, merges metadata on update), `List` (sorted by `updated_at` desc, exact cleaned `RepoPath` filter), `ReadPlan`, and `SetPhase`. Store roots must be absolute; an injectable clock keeps ID generation testable.
- `flowstore/` persists task-centric Flow records beside sessions and plans under `<root>/flows/<flow-id>/meta.json` (dirs `0700`/files `0600`, atomic writes). `Store.Create` requires title, instructions, and an absolute repo path, generates timestamp+slug IDs, seeds the default phase graph (plan, plan review, implementation, review loop, PR creation, autoreview, merge), and derives status from phases/merge metadata. Plan Review gates Implementation: only `approved`/`approved_with_concerns` completed outcomes, or skipped-with-notes, make Implementation ready. `AddChildPhase` creates or updates ordered child phases under Implementation; those children gate review loop and PR creation until completed or skipped with notes. `SetPlanLink` records linked saved-plan artifacts with locking and absolute plan-path validation, while `List` filters by exact cleaned `RepoPath` and sorts by `updated_at` desc.
- `model/` owns Bubble Tea state, key handling, fuzzy filtering, modal prompts, pager launches, confirmations, and async result handling (split across `model.go`, `model_keys.go`, `model_fetch.go`, `model_messages.go`, `model_filter.go`). Each list (repos, branches, stashes, worktrees, commits, reflog, sessions, plans, flows) is a generic value-type `pane.Pane[T]` from `model/pane/` that owns its own filtering, selection, and scroll, and prompt state (confirmations, input prompts, agent selection) is consolidated into a typed `modal.Modal` from `model/modal/`. Read-only content views (diffs, transcripts, summaries, plan bodies) launch `less -R` through `actions.PageText` with stale-result protection via an active view request id. The sessions pane is right-pane mode 6; `enter` pages the normalized transcript, `r` resumes the stored provider session, `s` pages the summary, and `y` copies the raw provider session ID. The plans pane is right-pane mode 7; `enter` expands/collapses phase rows, `o` pages the Markdown, `i` edits launch instructions before launching the selected agent, and `y` copies the plan Markdown path. The flows pane is right-pane mode 8; it shows linked plan and current phase state, `x` expands/collapses phase detail rows for the selected Flow, `o` pages the linked plan body or reports when no plan is linked, and `a` launches the selected ready Flow phase while recording launch metadata.
- `ui/` is stateless rendering with lipgloss, driven by a `RenderParams` snapshot.
- `internal/version/` holds version/commit/date injected via `-ldflags` at build time.

## Working Notes

- Tests use real temporary git repositories and command execution, not mocks; `gitquery` also accepts a fake `Runner` via `NewQuerier` for unit-level coverage.
- Destructive actions are gated by destructive mode in the model; preserve that safety boundary.
- Locked worktrees should not be deleted or pruned; unlock is a separate action.
- Branch lists hide non-root worktree branches; the root branch stays pinned at the top.
- `WORKTREE_ROOT` overrides `[scan].root`, which falls back to `~/dev`; `TERMINAL` overrides the terminal launched by `t` outside tmux/Zellij.
- `[scan].root` and `[sessions].root` support `~` expansion; `[sessions].root` must resolve to an absolute path. Parsed editor/terminal/provider/launch config fields are foundation only until their behavior is wired in.
- Agent session transcripts may contain secrets; wtui stores them under user state (`$XDG_STATE_HOME/wtui/sessions/v1` or `~/.local/state/wtui/sessions/v1`) with restrictive permissions, not inside repositories. Raw provider transcript copies are disabled by default; `copy_raw_transcripts = true` opts into preserving raw provider JSONL alongside normalized transcript data.
- Resuming a captured session from mode 6 uses the stored provider and raw `SessionID`. The launch command runs from the recorded `CWD` when present, falling back to `WorktreePath`; `WTUI_WORKTREE_PATH` remains the stored worktree metadata.
- Saved plans and Flows share the session artifact root (`<root>/plans/...` and `<root>/flows/...`), so moving or cleaning that root—including via `WTUI_FLOW_STATE_ROOT`, `WTUI_PLAN_STATE_ROOT`, or `WTUI_SESSION_STATE_ROOT` in TUI startup—also moves or removes saved artifacts. v1 has no TUI plan editing/deletion; TUI Flow mutation is limited to new Flow creation and ready-phase launches that mark the phase running, while agents persist plans through `wtui plan`, add child implementation phases with `wtui flow phase add-child`, and advance Flow phases through `wtui flow phase set`. The canonical plan skill source lives in `agent-skills/wtui-plan-persist/`, a non-auto-discovered repo directory intended to be symlinked into user-level Codex/Claude skill dirs. Plan-root precedence: `--state-root` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` > `[sessions].root` > default. Flow-root precedence: `--state-root` > `WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` > `[sessions].root` > default. Agent launches and session resumes set both `WTUI_SESSION_STATE_ROOT` and `WTUI_PLAN_STATE_ROOT` to the resolved root.
- CI expects `gofmt`, `make test`, and `make build` to pass.
