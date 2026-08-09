# Agent Operations

Build, test, and development workflow for `approach`. `AGENTS.md` links here; keep the authoritative command list and workflow detail in this file, not in the entrypoints.

## Commands

```bash
make build          # build bin/approach
make test           # run all tests
make run            # build and run the TUI
go test ./scanner   # run one package
gofmt -l .          # formatting check used by CI
```

CI requires clean `gofmt -l .`, `make test`, and `make build`.

## Development Workflow

- TDD by default (red → green → refactor). Tests use real temporary git repositories and command execution, not mocks; `gitquery` also accepts a fake `Runner` for unit-level coverage.
- Pull latest from main before starting changes, unless a different base is given.
- Never commit or push directly to main; branch first.
- Run `gofmt -l .`, `make test`, and `make build` before shipping.

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
- TUI mutation of plans and Flows is intentionally minimal (new Flow creation, ready-phase launches); agents persist everything else through the `approach plan` and `approach flow` CLIs. Canonical skill sources live in `agent-skills/approach-plan-persist/`, `agent-skills/approach-flow/`, and `agent-skills/approach-flow-create/` — non-auto-discovered, intended to be symlinked into user-level skill dirs; `agent-skills/skill_docs_test.go` asserts they stay in sync with the CLI contract.
