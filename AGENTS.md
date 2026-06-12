# AGENTS.md

## Commands

```bash
make build          # build bin/wtui
make test           # run all tests
make run            # build and run the TUI
go test ./scanner   # run one package
gofmt -l .          # formatting check used by CI
```

CI requires clean `gofmt -l .`, `make test`, and `make build`.

## Project Shape

`wtui` is a Go Bubble Tea TUI for managing git worktrees across repositories. User-facing behavior, key bindings, and CLI examples are documented in `README.md`; config reference is `docs/config.md`; Flow phase semantics are `docs/flow-phases.md`.

- `cmd/wtui/` — entrypoint. Handles `--version`, `session-hook --provider claude|codex`, and the `plan save|list|read|phase set` and `flow create|list|read|phase complete|phase block|phase needs-attention|phase restart|phase set|phase add-child|plan set|pr set|merge set` subcommands (`plan.go`, `flow.go`). Subcommands resolve the artifact root without scanning repos or starting the TUI.
- `config/` — optional TOML from `$XDG_CONFIG_HOME/wtui/config.toml` or `~/.config/wtui/config.toml`. Missing config is non-fatal; an existing but unreadable or malformed config is startup-fatal.
- `scanner/` — discovers repos under `WORKTREE_ROOT`, `[scan].root`, or `~/dev` (default depth 2, reducible via `[scan].max_depth`), excluding `*-worktrees`.
- `gitquery/` — read-only git queries. `parse.go` is pure parsing; `runner.go` defines the `Runner` seam wrapped by a `Querier` (`NewQuerier` injects a fake `Runner` for tests).
- `actions/` — git mutations (worktree create/remove/prune/unlock, branch delete, stash drop, fetch `--prune`, pull `--ff-only`), clipboard, VS Code, editor commands, tmux/Zellij/terminal launches, and Codex/Claude launch/resume command construction with wtui hook metadata.
- `agent/` — supported agent command names (`codex`, `codex-app`, `claude`) with normalization and validation.
- `sessions/` — agent-session metadata and normalized transcripts under the user state dir; ingests Claude/Codex hook payloads. Blank or whitespace-only session IDs are rejected at ingest and in the store; session dirs are keyed by a hash of the provider session ID.
- `embeddedterm/` — runtime-only PTY process management for embedded CLI agents (sessions-mode resumes and flows-mode headless phase launches). It owns PTY start, output capture, live visible lines, input forwarding, resize, lifecycle state, and termination behind a small API so model tests can fake terminals.
- `planstore/` — saved plans at `<root>/plans/<plan-id>/` (`meta.json` + `plan.md`).
- `flowstore/` — task-centric Flow records at `<root>/flows/<flow-id>/meta.json` with a seeded phase graph (plan → plan review → implementation → review loop → PR creation → autoreview → merge). The canonical transition table is `flowstore/transitions.go` (`AllowedNextPhaseStatuses`, `AgentSettablePhaseStatuses`); gating and readiness rules are in `docs/flow-phases.md`.
- `internal/artifacts/` — shared filesystem mechanics for the three stores: artifact-root resolution, absolute-root checks, `0700` dirs, `0600` atomic writes, safe IDs, phase-ID normalization (`NormalizePhaseID`), slugging, timestamp+slug collision allocation. Store-specific schemas, locking, and phase semantics stay in the owning store packages.
- `model/` — Bubble Tea state and key handling. Each list is a generic value-type `pane.Pane[T]` from `model/pane/`; prompt state is a typed `modal.Modal` from `model/modal/`; read-only views page through `less -R` via `actions.PageText` with stale-result protection. Flow start orchestration is grouped behind `FlowStarter` in `model/flow_start.go`; expanded Flow phase rows can resume attached provider sessions with fresh wtui launch tracking; full sessions-mode CLI resumes open runtime-only embedded PTYs with a `ctrl+g` command prefix. In flows mode, `enter` on a selected launchable phase launches that phase; CLI providers launch headlessly by default in a flow-scoped embedded PTY (`codex exec` / `claude --print`), `h` toggles headless mode for external CLI launches, and `tab` toggles list/terminal focus while a Flow terminal is open. Embedded PTY startup failures and slot exhaustion mark the phase `needs_attention`. The production TUI starts in the flows pane (mode 8).
- `ui/` — stateless lipgloss rendering from a `RenderParams` snapshot, including Flow recovery labels (`recover-worktree`, `await-session`, `session-mismatch`, `missing-session-id`, `missing-pr`), embedded terminal headers/live lines, and the flows-mode list/terminal split pane.
- `internal/version/` — version/commit/date injected via `-ldflags`.

## Working Notes

- Tests use real temporary git repositories and command execution, not mocks; `gitquery` also accepts a fake `Runner` for unit-level coverage.
- Destructive actions are gated by destructive mode in the model; preserve that safety boundary. Locked worktrees are never deleted or pruned — unlock is a separate action.
- Branch lists hide non-root worktree branches; the root branch stays pinned at the top.
- Plan and Flow phase IDs are normalized (trimmed + lowercased) before matching, so case- or whitespace-variant IDs upsert the same logical phase, and updates collapse duplicate rows left by older records.
- `WORKTREE_ROOT` overrides `[scan].root`; `TERMINAL` overrides `[terminal].command` for launches outside tmux/Zellij. `[editor].command` is used for plans-pane Markdown editing and falls back to `EDITOR`; provider/launch config fields are parsed foundation only. All store roots must be absolute.
- Sessions, plans, and Flows share one artifact root (default `$XDG_STATE_HOME/wtui/sessions/v1` or `~/.local/state/wtui/sessions/v1`); moving or cleaning it moves saved artifacts too. TUI root precedence is `WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` > `[sessions].root` > default; CLI subcommands also accept `--state-root` where documented. CLI-launched agents get the resolved root exported; `codex-app` receives prompt-only launch metadata plus copyable `--state-root` command examples.
- Transcripts may contain secrets: keep them under user state with restrictive permissions, never inside repositories. Raw provider transcript copies are opt-in (`copy_raw_transcripts = true`).
- Embedded terminals (sessions-mode resumes and flows-mode headless launches) are runtime-only and not persisted or restored; quitting while any are running asks for confirmation before terminating them. Keep PTY mechanics in `embeddedterm`; model should own slot selection, scope (session vs flow) routing, `ctrl+g` prefix routing, picker/confirm state, and conversion from saved sessions to `actions.AgentCommand` contexts. `codex-app` session resumes and flow launches continue to use the external deep-link path.
- TUI mutation of plans and Flows is intentionally minimal (new Flow creation, ready-phase launches); agents persist everything else through the `wtui plan` and `wtui flow` CLIs. Canonical skill sources live in `agent-skills/wtui-plan-persist/` and `agent-skills/wtui-flow/` — non-auto-discovered, intended to be symlinked into user-level skill dirs; `agent-skills/skill_docs_test.go` asserts they stay in sync with the CLI contract.
- When a Flow is linked to a saved plan, transitioning a Flow phase to `completed` syncs a matching saved-plan phase with the same normalized phase ID to `completed`; missing plan phases are ignored, and sync failures mark the Flow phase `needs_attention`. Repeating `completed` for an already-completed Flow phase preserves that completed state even if the linked-plan sync later fails.
- Keep Flow launch prompts minimal: state the action plus essential location metadata (worktree, branch, commit). Don't embed plan bodies, phase history, or status-update recipes unless the phase can't work without them.
