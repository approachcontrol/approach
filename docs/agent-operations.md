# Agent Operations

Build, test, and development workflow for `approach`. `AGENTS.md` links here; keep the authoritative command list and workflow detail in this file, not in the entrypoints.

## Commands

```bash
make build          # build bin/approach
make test           # run all tests
make fmt-check      # formatting check used by CI
make run            # build and run the TUI
go test ./scanner   # run one package
```

CI requires `make fmt-check`, `make test`, and `make build`. `web/node_modules`
is inside the repo tree and can contain Go files from npm packages, so it is
excluded twice over: the go command skips it via `ignore web/node_modules` in
`go.mod` (bare `go test ./...` included), while `gofmt` does not read `go.mod`,
so only `make fmt-check` excludes it — a plain `gofmt -l .` still descends.

The Next.js viewer in `web/` is outside the Go toolchain and has its own CI job
(`npm ci && npm run lint && npm run typecheck && npm test && npm run build`, run
from `web/`). Only run it when you change files under `web/`; see
`web/README.md`.

## Development Workflow

- TDD by default (red → green → refactor). Tests use real temporary git repositories and command execution, not mocks; `gitquery` also accepts a fake `Runner` for unit-level coverage.
- Pull latest from main before starting changes, unless a different base is given.
- Never commit or push directly to main; branch first.
- Run `make fmt-check`, `make test`, and `make build` before shipping.
- **A development build has its own artifact root.** `make build` stamps
  `version=dev`, and any binary whose version is not a published release tag
  (`vX.Y.Z`) defaults to `$XDG_STATE_HOME/approach-dev/sessions/v1` (or
  `~/.local/state/approach-dev/sessions/v1`) instead of the release-owned
  `.../approach/sessions/v1`. Only the *default* changes: `--state-root`, the
  `APPROACH_*_STATE_ROOT` variables, and `[sessions].root` are untouched, so
  Flow-launched agents — which always receive an explicit root — are unaffected.
  - The blast radius is wider than Flows. The same default backs the session
    store and the plan store, so a development build gets its own session
    history and plan list too. That is the intended outcome — development state
    is isolated as a unit rather than split across two roots — but the first run
    of a dev build shows empty Flow, plan, *and* session lists.
  - Existing development state under the release root is **not** migrated or
    copied. Operators who want it keep passing an explicit `--state-root`.
  - A development build additionally refuses to advance the database schema of
    the release default root, naming `--allow-dev-live-migration` (or
    `APPROACH_ALLOW_DEV_LIVE_MIGRATION=1`) as the acknowledgement. Reading an
    already-current release database is unaffected. This is the guard for the
    incident that motivated it: a dev build migrated the live root to a newer
    schema, and the released `approach` agents resolved from `PATH` could no
    longer open it, so successful phase work could not be persisted at all.

## Beads Task Tracking

This repository uses `bd` for durable task tracking. The command workflow lives
in `.agents/skills/beads/SKILL.md`; run `bd prime` when session context is
missing or stale. Track shared tasks, blockers, follow-up work, and persistent
project knowledge in Beads rather than ad hoc TODO or memory files.

The default profile is conservative: close completed Beads only after relevant
validation passes, inspect `git status` at handoff, and do not commit, push, or
sync the Dolt remote unless the user, orchestrator, or an explicit repository
profile grants that authority.

## Safety in Practice

- Destructive git actions are gated by destructive mode in the model — preserve that boundary. Locked worktrees are never deleted or pruned; unlock is a separate action.
- Transcripts may contain secrets: keep them under user state with restrictive permissions, never inside repositories. Raw provider transcript copies are opt-in (`copy_raw_transcripts = true`).
- TUI mutation of plans and Flows is intentionally minimal (new Flow creation, ready-phase launches); agents persist everything else through the `approach plan` and `approach flow` CLIs. Canonical skill sources live in `agent-skills/approach-plan-persist/`, `agent-skills/approach-flow/`, and `agent-skills/approach-flow-create/` — non-auto-discovered, intended to be symlinked into user-level skill dirs via `agent-skills/install.sh` (`--dry-run` to preview); `agent-skills/skill_docs_test.go` asserts they stay in sync with the CLI contract, and `agent-skills/install_test.go` covers the installer.
