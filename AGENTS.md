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

- `cmd/wtui/main.go` wires `scanner.Scan()` into the Bubble Tea program.
- `scanner/` discovers repos under `WORKTREE_ROOT` or `~/dev`, excluding `*-worktrees`.
- `gitquery/` shells out to git and parses worktrees, branches, stashes, history, and reflog data.
- `actions/` performs git mutations plus clipboard, VS Code, and tmux/Zellij terminal launching.
- `model/` owns Bubble Tea state, key handling, overlays, confirmations, and async result handling.
- `ui/` is stateless rendering with lipgloss.

## Working Notes

- Tests use real temporary git repositories and command execution, not mocks.
- Destructive actions are gated by destructive mode in the model; preserve that safety boundary.
- Locked worktrees should not be deleted or pruned; unlock is a separate action.
- Branch lists hide non-root worktree branches; the root branch stays pinned at the top.
- CI expects `gofmt`, `make test`, and `make build` to pass.
