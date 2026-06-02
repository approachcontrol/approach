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

- `cmd/wtui/main.go` parses `--version`/`-v`, reads `WORKTREE_ROOT`, and wires `scanner.Scan()` into the Bubble Tea program.
- `scanner/` discovers repos under `WORKTREE_ROOT` or `~/dev` (up to 2 levels deep), excluding `*-worktrees`.
- `gitquery/` shells out to git for worktrees, branches, stashes, history, reflog, and diffs; `parse.go` holds the pure parsing split out from execution.
- `actions/` performs git mutations (create/remove/prune/unlock worktree, delete branch, drop stash, fetch `--prune`, pull `--ff-only`) plus clipboard, VS Code, and tmux/Zellij terminal launching.
- `model/` owns Bubble Tea state, key handling, fuzzy filtering, overlays, confirmations, and async result handling (split across `model.go`, `model_keys.go`, `model_fetch.go`, `model_messages.go`, `model_filter.go`).
- `ui/` is stateless rendering with lipgloss, driven by a `RenderParams` snapshot.
- `internal/version/` holds version/commit/date injected via `-ldflags` at build time.

## Working Notes

- Tests use real temporary git repositories and command execution, not mocks.
- Destructive actions are gated by destructive mode in the model; preserve that safety boundary.
- Locked worktrees should not be deleted or pruned; unlock is a separate action.
- Branch lists hide non-root worktree branches; the root branch stays pinned at the top.
- `WORKTREE_ROOT` overrides the scan root (default `~/dev`); `TERMINAL` overrides the terminal launched by `t` outside tmux/Zellij.
- CI expects `gofmt`, `make test`, and `make build` to pass.
