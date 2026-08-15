# Bugbot rules for Approach

Approach is a Go Bubble Tea TUI for git worktrees, plus a read-only GraphQL API (`approach serve`) and a separately deployed Next.js viewer in `web/`.

## Review posture

Flag real bugs, security regressions, and broken load-bearing invariants. Skip gofmt/style nits, "add more comments," and docs wording unless the docs now contradict the code. A finding should name a concrete failure: leaked secrets, deleted locked worktree, stuck Flow, unbounded GraphQL result, or a write that lands in a repository.

## Never break

- Destructive git actions stay behind destructive mode. Locked worktrees are never deleted or pruned; unlock is a separate action.
- Transcripts and session metadata stay under the user state dir with restrictive permissions (`0700` dirs, `0600` files). Never write them into a scanned repository.
- Store roots must be absolute. Artifact writes stay atomic.
- Plan and Flow phase IDs are normalized (trim + lowercase) before matching.
- `gitquery` and `beadsquery` stay read-only. `beadsmutate` is the only Beads write adapter, and its only operation is `bd update --claim`.
- Exactly one live `flowstore.Store` per process. Flow writers must not hold the SQLite writer across foreign I/O.
- Exactly one launch-lifecycle attempt owns a Flow at a time, and ownership must not lapse. Repair launches export an empty phase ID and never call `AddPhaseLaunchID`.
- Tmux live-window probes belong only on one-shot user-initiated choke points, never in render-path predicates. A failed probe reports "no live window"; do not replace it with a launch-ID fence that can strand a phase.
- The GraphQL API is structurally read-only: no `Mutation` or `Subscription`, non-query operations rejected, and phase `sessions`, `launch_ids`, and transcript paths stay out of the schema. Never weaken query depth, node, cost, body-size, in-flight, or CSRF (`application/json` only, no CORS headers) limits.
- Non-loopback `approach serve` requires a token. Tokens belong in `APPROACH_API_TOKEN`, not checked-in config. The web viewer keeps the endpoint and token server-side and must not select `Flow.instructions` or `Phase.notes`.

## Path notes

- `model/` and `flowstore/`: treat launch ownership, receipt/progression triggers, and phase-transition tables as correctness, not style.
- `graphqlapi/`: treat auth, Host allowlisting, and result-amplification limits as security findings when loosened.
- `web/`: separate product. Do not mix it into the Go toolchain. Do not commit generated `web/AGENTS.md`, `web/CLAUDE.md`, or incidental `web/next-env.d.ts` churn.
- `actions/` and `scanner/`: tests use real temporary git repos. A change that deletes or prunes without the destructive/lock gates is a bug even if tests still compile.

## Ignore

- Formatting that `make fmt-check` already owns.
- Test helpers that talk to real git or `bd` instead of mocks.
