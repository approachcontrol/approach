# Agent Operations

Build, test, and development workflow for `wtui`. `AGENTS.md` links here; keep the authoritative command list and workflow detail in this file, not in the entrypoints.

## Commands

```bash
make build          # build bin/wtui
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

## Safety in Practice

- Destructive git actions are gated by destructive mode in the model — preserve that boundary. Locked worktrees are never deleted or pruned; unlock is a separate action.
- Transcripts may contain secrets: keep them under user state with restrictive permissions, never inside repositories. Raw provider transcript copies are opt-in (`copy_raw_transcripts = true`).
- TUI mutation of plans and Flows is intentionally minimal (new Flow creation, ready-phase launches); agents persist everything else through the `wtui plan` and `wtui flow` CLIs. Canonical skill sources live in `agent-skills/wtui-plan-persist/` and `agent-skills/wtui-flow/` — non-auto-discovered, intended to be symlinked into user-level skill dirs; `agent-skills/skill_docs_test.go` asserts they stay in sync with the CLI contract.
