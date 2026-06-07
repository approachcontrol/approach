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

- `cmd/wtui/main.go` parses `--version`/`-v`, handles `session-hook --provider claude|codex` and the `plan save|list|read|phase set` subcommands, loads config, applies `WORKTREE_ROOT`, and wires `scanner.Scan()` into the Bubble Tea program. Plan subcommands live in `cmd/wtui/plan.go` and resolve the artifact root without scanning repos or starting the TUI.
- `config/` loads optional TOML from `$XDG_CONFIG_HOME/wtui/config.toml` or `~/.config/wtui/config.toml`; missing config is non-fatal, unreadable or malformed existing config is startup-fatal, and parsed fields include scan, editor, terminal, provider, launch, agent, sessions, and bootstrap settings.
- `scanner/` discovers repos under `WORKTREE_ROOT`, `[scan].root`, or `~/dev` (default up to 2 levels deep, configurable down with `[scan].max_depth`), excluding `*-worktrees`.
- `gitquery/` shells out to git for worktrees, branches, stashes, history, reflog, and diffs; `parse.go` holds the pure parsing split out from execution, and `runner.go` defines the `Runner` seam (the git CLI adapter) wrapped by a `Querier`. Package-level functions delegate to a default `Querier`; `NewQuerier(runner)` injects a fake `Runner` for tests.
- `actions/` performs git mutations (create/remove/prune/unlock worktree, delete branch, drop stash, fetch `--prune`, pull `--ff-only`) plus clipboard, VS Code, and tmux/Zellij terminal launching.
- `sessions/` stores portable agent-session metadata and normalized transcripts under the user state directory, ingests Claude/Codex hook payloads, resolves git metadata, and exposes `Store` methods for list/read/finalize behavior. Session directories are keyed by a hash of provider session ID rather than the raw ID, and store roots must be absolute.
- `planstore/` persists saved plans beside sessions under the shared agent-artifact root (`<root>/plans/<plan-id>/meta.json` + `plan.md`, dirs `0700`/files `0600`, atomic writes). `Store` exposes `Save` (validates title/content/status, generates timestamp+slug IDs with collision suffixes, merges metadata on update), `List` (sorted by `updated_at` desc, exact cleaned `RepoPath` filter), `ReadPlan`, and `SetPhase`. Store roots must be absolute; an injectable clock keeps ID generation testable.
- `model/` owns Bubble Tea state, key handling, fuzzy filtering, overlays, confirmations, and async result handling (split across `model.go`, `model_keys.go`, `model_fetch.go`, `model_messages.go`, `model_filter.go`). Each list (repos, branches, stashes, worktrees, commits, reflog, sessions, plans) is a generic value-type `pane.Pane[T]` from `model/pane/` that owns its own filtering, selection, and scroll, and overlay state (confirmations, input prompts, diffs/transcripts, plain-text plan bodies) is consolidated into a typed `modal.Modal` from `model/modal/`. The plans pane is right-pane mode 7 with a distinct `modal.Text` overlay.
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
- Saved plans share that artifact root (`<root>/plans/...`), so moving or cleaning the sessions root—including via `WTUI_PLAN_STATE_ROOT`—also moves or removes saved plans. v1 has no TUI plan editing or deletion: agents persist plans through the `wtui plan` CLI, guided by the repo-scoped `wtui-plan-persist` skill in `.agents/skills/`. Plan-root precedence: `--state-root` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` > `[sessions].root` > default. Agent launches set both `WTUI_SESSION_STATE_ROOT` and `WTUI_PLAN_STATE_ROOT` to the resolved root.
- CI expects `gofmt`, `make test`, and `make build` to pass.
