# Agent Operations

Build, test, and development workflow for `approach`. `AGENTS.md` links here; keep the authoritative command list and workflow detail in this file, not in the entrypoints.

## Commands

```bash
make build          # build bin/approach
make test           # run all tests
make fmt-check      # formatting check used by CI
make run            # build and run the TUI
go test ./scanner   # run one package
go test -short ./... # skip the packages that build a second binary
```

`make test` takes roughly 7-8 minutes. `actions` and `gitquery` spin up real
temporary git repositories, and `internal/regression` adds one `go build` of
`cmd/approach` per run (~20-40s): it is the cross-package incident suite, and
running the real binary against the real generated prompt is the whole reason
it exists. `-short` skips that package entirely when you are iterating.

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
- Claude's code review does not run automatically on a pull request. Ask for one
  with `gh workflow run claude-code-review.yml -f pr_number=<N>` (or the Actions
  tab), which works for drafts too; the workflow refuses a non-numeric input and
  refuses a pull request whose head is a fork. `@claude` mentions on an issue or
  pull request are a separate workflow and still work as before.
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
approach db restore --backup PATH     # put a verified pre-migration backup back
```

All three accept `--state-root PATH`. A TUI start also migrates; nothing else
does.

`approach db restore` is the reversible half. It verifies the backup before it
touches anything (`integrity_check`, plus the object shape for the backup's own
`user_version`), refuses while **any** process holds the database open — a
restore replaces the file, so every live holder ends up on an unlinked inode,
which is why this is stricter than migration's refusal — takes the bootstrap
lock, then refuses a backup that is not the one the current generation was
migrated from unless `--force` acknowledges it. Two things enforce that "any
holder" refusal, because one cannot: the owners lease names long-lived holders,
and a connection probe (`locking_mode=exclusive` with `busy_timeout(0)`, which
SQLite answers `SQLITE_BUSY` for exactly while another connection is attached)
catches the short-lived `flow` and `plan` leaves, which publish no lease and can
be mid-command. Only a busy answer refuses; a missing, read-only, or damaged
database is inconclusive, and a restore is the command those cases need. It
copies the database it replaces into `backups/` first, so a restore is itself
reversible. That copy is checkpointed so it is self-contained; when SQLite
cannot open the database to checkpoint it, the `-wal` — where committed rows can
live alone after a crash — is copied beside it instead, and a backup carrying
its own `-wal` is promoted with it. The copy's name is reserved with exclusive
creation, so two restores in the same second cannot overwrite each other's
recovery copy, and the replaced database's stale `-wal`/`-shm` are removed (safe
only there: the lock is held and nothing has the database open) so the restored
file is not shadowed by the replaced one's uncheckpointed content. `--json`
reports in `db inspect`'s key style.

**Migration is blocked while a long-lived owner holds the database at an older
build.** The TUI and `approach serve` publish a holder file under
`<root>/.approach.db.owners/`, each holding an exclusive `flock` on its own file
for the process lifetime; short-lived `flow` and `plan` leaves publish nothing.
A migrator scans that directory *before* taking the bootstrap lock — the total
acquisition order is owners lease, then bootstrap lock, never the reverse — and
only when a lock-free `user_version` read says the schema would actually
advance, so an ordinary current-schema open reads nothing. The refusal names
every blocking holder's PID, build, and executable in one message. Liveness is
proved by the lock rather than by a PID heuristic, so a crashed holder is reaped
by the next scan and blocks nothing. `db inspect` reports them as `owners`, and
unlike `migration_owner` those are marked `verified`.

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
- **A migration records what it did, and proves the result is usable before it
  reports success.** `approach.db.meta.json` carries an append-only `history`
  array (build, commit, timestamp, from/to versions, backup path, generation,
  provenance), capped at 100 entries with a `history_truncated` flag. After the
  commit and *before* the bootstrap lease is released, the migration re-checks
  the schema and round-trips a sample of records through the read path's codec;
  a failure names the backup it just wrote and the exact `approach db restore`
  command that undoes it. Records that were already undecodable before the
  migration are tolerated — they are a partial list on the read path, and
  refusing an upgrade over one would strand exactly the corpus that most needs
  migrating.
- **A live handle is fenced if its database changes underneath it.** Every write
  re-reads `user_version` (a same-connection pragma) and re-reads the sidecar's
  generation at most once a second, or on demand via `Store.Revalidate()`.
  Either moving aborts that write and every one after it with
  `flowstore.ErrDatabaseGenerationChanged`, naming both values. Reads are
  deliberately not fenced.

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
- TUI mutation of plans and Flows is intentionally minimal (new Flow creation, ready-phase launches); agents persist everything else through the `approach plan` and `approach flow` CLIs. The canonical unified skill lives in `agent-skills/approach-flow/` — non-auto-discovered and intended to be symlinked or copied into a user-level skill directory; `agent-skills/unified_skill_test.go` asserts that its package structure and core CLI workflow remain intact.
