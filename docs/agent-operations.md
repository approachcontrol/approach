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
    Since role separation the acknowledgement's reach is narrower: it governs
    *creation* on every command, but it no longer lets `approach flow` or
    `approach serve` migrate a predecessor-schema root, because those return the
    role refusal first. `approach db migrate` and the TUI are the two surfaces
    that migrate, and both read it.

## Migrating the Flow Database

**Migration is not automatic.** After a release that bumps the flow database
schema, `approach flow`, `approach serve`, and the `SessionEnd` session hook all
refuse the root until it is migrated:

```bash
approach db inspect --json            # what is in this root, and can approach open it
approach db migrate                   # advance the schema (the only CLI entry point)
approach db migrate --backup-dir DIR  # write the pre-migration backup somewhere else
```

Both accept `--state-root PATH`. A TUI start also migrates; nothing else does.

What each open is allowed to do is named at the call site as a
`flowstore.StoreOptions.Role`, and a call-site test fails the build if a
non-test `NewStore` omits one:

- `RoleMigrator` — TUI startup and `approach db migrate`. The only role that
  advances the schema, imports a legacy `flows/` corpus, resumes an interrupted
  cutover, or writes the `approach.db.meta.json` provenance sidecar.
- `RoleWriter` — every mutating `flow` leaf, the hook's session attachment, and
  the TUI's lazy fallback store.
- `RoleReader` — `flow list`, `flow read`, `approach serve`, and the hook's
  launch-staleness resolver. Opens `mode=ro`, refuses writes in Go before SQLite
  sees them, and mutates nothing on the open path.

Three consequences worth knowing:

- `flow list` and `approach serve` **no longer tighten a loose state root to
  `0700`**. They report the mode instead — printed to stderr on every reader
  open, and available through `Store.OpenDiagnostics()` and `db inspect`'s
  `directory_mode` — because repairing it would erase the very state the
  diagnostic exists to show. A first `approach serve` against a `0755` root
  therefore leaves a `0600` database inside a `0755` directory; the file mode is
  the one that matters and is unchanged.
- **The TUI's lazy fallback store cannot migrate.** It is a `RoleWriter`, so a
  TUI that reaches it against a predecessor-schema root refuses and points at
  `approach db migrate` — from inside the alt screen, where that is awkward to
  act on. One migrator per process is the intended answer; if the refusal proves
  unusable the fix is a TUI-level prompt, not a silently re-widened fallback.
- **Every real migration writes a verified backup** to `<root>/backups/`
  (`VACUUM INTO`, then integrity, schema-stamp, and row-count checks), keeping
  the 8 most recent per migrated file per state root. That roughly doubles peak
  disk, so an upgrade on a full disk now fails at the backup with `user_version`
  unchanged rather than migrating. `--backup-dir` is the escape hatch; there is
  no skip flag. Backup names carry a fingerprint of the root they came from, so
  one `--backup-dir` shared by several state roots neither collides nor lets one
  root's migration prune another's copies. The copy is built in a `0700` staging
  directory and renamed into place, so a custom backup directory never holds a
  world-readable copy mid-write. If another process commits between the backup
  and the migration's write lock — an older build, which does not honour the
  migration lease — the migration aborts with nothing changed rather than
  publishing a backup that does not match what it migrated.

`approach db inspect` never refuses. It never constructs a store, never takes
the bootstrap lock, and never repairs anything, so it still answers while a
migration is running, against a database from a newer build, and against a root
whose directory is read-only. Its `tier` is one of `missing`, `open`,
`not_writable`, `malformed`, `not_a_database`, or `header`, and `reason` and
`next_action` are non-null exactly when `readable` is false. `open` means the
store really opens: a database stamped at this build's schema is also checked
for the shape `NewStore` requires, so one missing its `flows` table or an index
reports `malformed` rather than contradicting the open that then fails. A
predecessor or newer-build schema is classified by `user_version` alone.

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
