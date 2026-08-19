# Agent Entry Point for Approach

Routing only. Substance lives in the linked docs; anything duplicated here drifts.
CLAUDE.md is a symlink to this file — edit here.

## Read First

- Commands, quality gate, dev workflow, safety: `docs/agent-operations.md`
- Architecture, package map, invariants: `docs/architecture.md`
- Overview, install, quick start: `README.md`
- TUI behavior and key bindings: `docs/tui-guide.md`
- Agent session hooks and storage: `docs/agent-sessions.md`
- Config reference: `docs/config.md`
- Flow phase semantics: `docs/flow-phases.md`
- Read-only GraphQL API (`approach serve`): `docs/graphql-api.md`
- Beads task tracking: `.agents/skills/beads/SKILL.md` (run `bd prime` for live session context)
- Unified Flow/plan skill recipe: `agent-skills/approach-flow/SKILL.md`

## Non-negotiables

Branch first — never commit or push directly to main. Everything else (TDD, quality gate, Flow/plan persistence, transcript safety) is in `docs/agent-operations.md`.

Approach is a single-user personal tool. This means no backwards-compat shims, no data-migration paths, no deprecation periods. Make clean breaks and say so in the PR.

## Conflict Rule

If this file conflicts with a linked doc, trust the linked doc and remove the duplicate here.

## Cursor Cloud specific instructions

Startup runs the update script (`go mod download`; `npm ci` in `web/` when its
lockfile is present), so Go modules and `web/node_modules` are already fetched.
Two independent products live here; standard commands are already documented —
use them rather than duplicating:

- Go TUI (`approach`, repo root): build/test/fmt/run commands are in
  `docs/agent-operations.md` and the `Makefile`.
- Next.js web viewer (`web/`): lint/typecheck/test/build/dev commands are in
  `web/README.md`. It is a separate deployable with its own CI job.

Non-obvious caveats worth knowing:

- `make test` is slow (roughly 6–7 min): `actions` and `gitquery` spin up real
  temporary git repositories. When iterating, run one package (e.g.
  `go test ./scanner`) instead of the full gate.
- `bd` (beads) is not installed in this environment. The TUI degrades gracefully
  to a calm `beads not configured` state, and the Beads task-tracking commands in
  the blocks below will not run — don't block on them for env work.
- The TUI is a full-screen Bubble Tea app that needs a real TTY. Run it under
  tmux with an explicit size (e.g. `tmux new-session -x 200 -y 50 ... './bin/approach'`)
  and inspect it with `tmux capture-pane`; it will not render in a plain
  non-TTY shell.
- To exercise the web viewer end-to-end: start `approach serve` (see
  `docs/graphql-api.md`) against scratch `--state-root`/`--scan-root` dirs (never
  the default artifact root), point `web/.env.local`'s `APPROACH_GRAPHQL_URL` at
  it, then `npm run dev` in `web/`. `scripts/generate-test-repos.sh` creates demo
  repos, and `approach flow create ... --state-root <dir>` seeds a Flow to view.
- `npm run dev`/`next build` regenerate `web/AGENTS.md`, `web/CLAUDE.md`, and
  touch `web/next-env.d.ts`. These are Next.js artifacts — do not commit them.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:970c3bf2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   bd dolt push
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->

<!-- BEGIN BEADS CODEX SETUP: generated by bd setup codex -->
## Beads Issue Tracker

Use Beads (`bd`) for durable task tracking in repositories that include it. Use the `beads` skill at `.agents/skills/beads/SKILL.md` (project install) or `~/.agents/skills/beads/SKILL.md` (global install) for Beads workflow guidance, then use the `bd` CLI for issue operations.

### Quick Reference

```bash
bd ready                # Find available work
bd show <id>            # View issue details
bd update <id> --claim  # Claim work
bd close <id>           # Complete work
bd prime                # Refresh Beads context
```

### Rules

- Use `bd` for all task tracking; do not create markdown TODO lists.
- Run `bd prime` when Beads context is missing or stale. Codex 0.129.0+ can load Beads context automatically through native hooks; use `/hooks` to inspect or toggle them.
- Keep persistent project memory in Beads via `bd remember`; do not create ad hoc memory files.

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.
<!-- END BEADS CODEX SETUP -->
