# GraphQL API

`approach serve` exposes a **read-only** GraphQL view of the repositories
Approach discovers and the Flow records it persists. It is a separate process
from the TUI: both read the same artifact root, and `internal/artifacts` writes
whole records atomically, so a running `approach serve` observes the same live
state as a running TUI without coordinating with it.

Package: `graphqlapi/`. Command: `cmd/approach/serve.go`.

## Running it

```bash
# Loopback default (127.0.0.1:8787), no token needed
approach serve

# Pick a free port; the resolved address is printed on stdout
approach serve --addr 127.0.0.1:0

# Any non-loopback bind requires a token
APPROACH_API_TOKEN=secret approach serve --addr 0.0.0.0:8787
```

| Setting | Precedence |
| --- | --- |
| Bind address | `--addr` > `APPROACH_API_ADDR` > `127.0.0.1:8787` |
| Token | `--token` > `APPROACH_API_TOKEN` > none (loopback only) |
| State root | `--state-root` > `APPROACH_FLOW_STATE_ROOT` > `APPROACH_PLAN_STATE_ROOT` > `APPROACH_SESSION_STATE_ROOT` > `[sessions].root` |
| Scan root | `--scan-root` > `WORKTREE_ROOT` > `[scan].root` > `~/dev` |

`[scan].max_depth` is honored exactly as in the TUI. There is no `[api]` config
section: a token belongs in the environment, not in a checked-in TOML file.

**Startup writes one thing.** "Read-only" describes the request path, not the
process. `flowstore.NewStore` calls `artifacts.EnsureCollection`, which
`MkdirAll`s `<root>/flows` and `chmod`s both the artifact root and that
directory to `0700`. That happens once at startup, never per request.

## Endpoints

| Path | Method | Auth | Notes |
| --- | --- | --- | --- |
| `/graphql` | `POST` | see below | The only query endpoint |
| `/healthz` | `GET` | never | Returns `{"status":"ok"}` and no state |

Anything else is a 404.

### Request shape

```json
{ "query": "...", "variables": { }, "operationName": "..." }
```

`query` is required. `variables` and `operationName` are both wired through, so
parameterized documents and multi-operation documents work as any GraphQL
client expects. Batched (array) requests are rejected.

```bash
curl -s -H 'Content-Type: application/json' \
  -d '{"query":"{ repos { id displayName inScanRoot } }"}' \
  http://127.0.0.1:8787/graphql

curl -s -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer '"$APPROACH_API_TOKEN" \
  -d '{"query":"query F($id: ID!) { flow(id: $id) { title status currentPhase { id status } } }","variables":{"id":"'"$FLOW_ID"'"}}' \
  http://127.0.0.1:8787/graphql
```

## Auth posture

- On a **loopback** bind the token is optional. When no token is configured the
  handler instead enforces a `Host` allowlist (`localhost`, `127.0.0.1`, `[::1]`,
  any port) to blunt DNS rebinding.
- On **any non-loopback** bind a token is required, and startup fails *before*
  the listener opens if it is missing.
- The token is accepted as `Authorization: Bearer <token>` or
  `X-Approach-Token`, compared with `crypto/subtle.ConstantTimeCompare`.
- **When a token is configured the `Host` header is not checked.** Rebinding is
  only a threat while the token is optional — a rebound browser cannot supply a
  bearer token it does not have. Checking `Host` unconditionally would 403 every
  request arriving through a tunnel, which is the recommended remote consumer
  shape below.
- `GET /healthz` is exempt from auth even when a token is configured.

### Read-only is structural

No `Mutation` or `Subscription` root type exists in the schema, and the handler
rejects any parsed operation whose type is not `query` before execution.

## Hardening

| Control | Rule | Response |
| --- | --- | --- |
| Method | `POST` only on `/graphql`; `GET` only on `/healthz` | 405 |
| Unknown path | Anything other than `/graphql` and `/healthz` | 404 |
| Content-type | `application/json` required; a `charset` parameter is allowed | 415 |
| `Host` header | Loopback allowlist, **only when no token is configured** | 403 |
| Body size | 64 KiB | 413 |
| Query depth | Max nesting 12, measured on the parsed AST with fragments resolved | 400 |
| Query size | Max 2000 field nodes after fragment expansion | 400 |
| Fragment cycle | Any cyclic fragment spread | 400 |
| In-flight cap | 8 concurrent snapshots | 503 |
| Request timeout | 20s around snapshot construction | 504 |
| Server timeouts | `ReadHeaderTimeout` 5s, `ReadTimeout` 15s, `WriteTimeout` 30s, `IdleTimeout` 60s | — |
| CORS | No `Access-Control-*` header is ever emitted, on any status | — |

### Limits and the introspection exemption

`graphql-go/graphql` has no built-in depth or complexity limiting, so
`graphqlapi/limits.go` walks the parsed AST itself. Two denial-of-service
shapes live here and the walk is the only thing in front of either:

- A **cyclic fragment spread** (`fragment A { flows { ...B } }`,
  `fragment B { repo { ...A } }`) would recurse forever. Cycles are rejected up
  front, over the whole spread graph, before any recursive measurement.
- An **acyclic doubling chain** (`F1 { ...F2 ...F2 }`, `F2 { ...F3 ...F3 }`, …)
  is a few hundred bytes that expands to billions of fields. Per-fragment
  memoization keeps the walk linear; the saturated node count then rejects the
  document. A hard budget of 20 000 node visits is the backstop.

Subtrees rooted at a field whose name starts with `__` are **exempt from the
depth limit**, so introspection and client codegen keep working. They still
count toward the node cap, which is what bounds `__type(name:)` recursion.

Inline fragments are depth-transparent: they add no nesting level.

### HTTP status contract

- **Transport, auth, and limit failures** get the 4xx/5xx status above with the
  body `{"errors":[{"message":"<fixed text>"}]}` and no `data` key. Messages are
  fixed strings, never derived from request content.
- **GraphQL parse, validation, and execution errors** on an otherwise
  well-formed request get **200** with `data: null` (or partial data) and a
  populated `errors` array — what every GraphQL client expects.

The limit walk parses the document itself. A *parse failure* there is not a
400: a syntactically invalid document is a GraphQL error, so the request falls
through to execution and gets the 200-with-`errors` response. Only depth,
node-count, and fragment-cycle rejections — all of which require a successful
parse — are 400s.

### Errors and logs

Store and scan failures are logged server-side with full detail and surface to
clients as the fixed message `internal error reading application state` — never
a filesystem path or `os` error text.

One log line per request: method, path, status, duration. Never the
`Authorization` or `X-Approach-Token` value, and never the query body. Request
logs go to stderr; only the resolved listen address goes to stdout.

## Live state and consistency

Each request builds **one snapshot** — one `scanner.Scan` plus one
`flowstore.List` — and resolves the whole query from it. A single response is
therefore internally consistent, and every new request re-reads disk. There is
no cross-request cache.

**A failing scan degrades; a failing store errors.** A missing or mistyped scan
root is the ordinary case, not an exceptional one, so a scan failure yields an
empty scanned-repo set plus one logged warning: flow-derived repos still
populate `repos`, and `flows` / `flow` are unaffected. A `flowstore.List`
failure is a real failure of the primary data source and surfaces as the
sanitized GraphQL error above.

**The scan cannot be cancelled.** `scanner.Scan` and `flowstore.List` take no
`context.Context` and walk the filesystem synchronously, so the request timeout
cannot interrupt them. The design is explicit about this:

1. The handler takes an in-flight semaphore slot *before* launching snapshot
   construction; if none is free it returns 503 immediately.
2. Snapshot construction runs in a goroutine; the handler waits on it against
   the timeout.
3. On timeout the handler returns 504 **while the slot is still held**. The slot
   is released by the snapshot goroutine when the scan finally finishes, never
   by the handler. Releasing on handler return would let a client that fires and
   abandons requests spawn unbounded concurrent orphaned scans, with 503 never
   firing — the two controls would cancel each other out.
4. Resolvers then run against the completed in-memory snapshot and are pure map
   and slice reads.

So the semaphore is the real backpressure control; the timeout only bounds how
long a client waits. `[scan].max_depth` bounds each scan.

## Schema

```graphql
scalar DateTime

type Query {
  repos: [Repo!]!
  repo(id: ID!): Repo
  flows(repoId: ID): [Flow!]!
  flow(id: ID!): Flow
}

type Repo {
  id: ID!
  path: String!
  displayName: String!
  isBare: Boolean!
  inScanRoot: Boolean!
  flows: [Flow!]!
}

type Flow {
  id: ID!
  title: String!
  instructions: String
  status: String!
  repoPath: String!
  repo: Repo!
  worktreePath: String
  branch: String
  baseRef: String
  commit: String
  presetName: String
  planId: String
  autoMode: Boolean!
  issue: Issue
  pullRequest: PullRequest
  merge: Merge
  phases: [Phase!]!
  currentPhase: Phase
  createdAt: DateTime!
  updatedAt: DateTime!
}

type Phase {
  id: ID!
  parentPhaseId: String
  title: String!
  kind: String!
  status: String!
  order: Int!
  dependsOn: [String!]!
  outcome: String
  notes: String
  summary: String
  createdAt: DateTime!
  updatedAt: DateTime!
}

type Issue { provider: String, number: Int, url: String }
type PullRequest { provider: String, number: Int, url: String, headBranch: String, baseBranch: String, status: String }
type Merge { status: String, commit: String, mergedAt: DateTime }
```

### IDs, traversal, and ordering

- `Repo.id` is the repository's **normalized absolute path**; `Flow.id` is the
  `flow_id`. Every path entering the index — from the scanner, from
  `FlowRecord.RepoPath`, and from the `repo(id:)` argument — goes through the
  same `filepath.Abs` + `filepath.Clean` rule, so an unnormalized `repo(id:)`
  argument still resolves and the same repo never appears twice.
- `Flow.repoPath` is emitted normalized, so `repo(id: flow.repoPath)` always
  resolves within the same response.
- The repo set is the scan results **unioned with** every distinct repo path
  referenced by a Flow. Flows can point outside the scan root, so synthesized
  entries get `displayName` = base name, `isBare: false`, `inScanRoot: false`.
  Because the union guarantees a match, `Flow.repo` is non-null.
- A Flow record with an **empty `repo_path` is skipped entirely** — it would
  otherwise synthesize a phantom repo at the server's working directory.
- `flows(repoId:)` and `repo { flows }` are the same lookup in the same
  per-request index, so one response cannot disagree with itself.
- Flows keep the store's order (`updated_at` descending). Repos are re-sorted
  across the whole union by `(lowercased displayName, path)`: the union cannot
  inherit the scanner's order, because synthesized entries were never sorted and
  `displayName` is not unique across the union (a depth-2 scanned repo is
  `parent/child` while a synthesized entry for the same repo is `child`).
- Unknown `repo(id:)` and `flow(id:)` resolve to `null`, not an error. Because
  IDs resolve only against the per-request snapshot, `repo(id:)` is not a
  filesystem-existence oracle.
- Timestamps are RFC3339 strings.
- Optional string fields are `null` when unset, never empty strings.

### `currentPhase` semantics

`currentPhase` is the first phase in graph order whose status is `ready`,
`running`, `needs_attention`, or `blocked`. It means **"the phase the Flow is
sitting on", not "the phase actively executing"** — `blocked` and
`needs_attention` count. It is `null` when no phase matches. The rule lives in
`flowstore.NextActionablePhase`, shared with the `approach flow` CLI.

### Nullability

| Field | Null when |
| --- | --- |
| `Flow.issue` | provider, number, and URL are all empty |
| `Flow.pullRequest` | every field is empty |
| `Flow.merge` | status is `pending` and commit and `mergedAt` are empty |
| `Merge.mergedAt` | the merge has not landed |
| `Flow.currentPhase` | no phase is actionable |

`Phase.dependsOn` is `[String!]!` and is **never** null: a phase with no edges
serializes as `[]`.

### Statuses are strings, not enums

`Flow.status`, `Phase.status`, `Phase.kind`, and `Phase.outcome` are `String`.
Most of those values do come from closed sets — see `flowstore/transitions.go`
and `docs/flow-phases.md` — but `Phase.outcome` is constrained only for
`plan_review` phases, and `PullRequest.status` is agent-supplied free text with
no validation. One unrecognized value in an enum field hard-fails serialization
for the *whole* response, which is a bad trade for a read API. Enums are a
follow-up once those two fields are constrained.

### Deliberately absent

Phase `sessions`, `launch_ids`, and any transcript path are **not in the
schema**. Transcripts may contain secrets, and the repo invariant keeps them out
of anything shareable (`docs/architecture.md`, `docs/agent-sessions.md`). Also
dropped, for lower-stakes reasons: `FlowRecord.PlanPath` (redundant with the
exposed `planId`, which is the stable identifier to use), `SchemaVersion` (a
storage detail), and `GraphRecovery` (not persisted).

## Consuming from a web app

A browser should not hold the token. The recommended shape is a **server-side
route or proxy** in the consuming app that holds the token and reaches Approach
over a tunnel. That works today with no CORS configuration, and the conditional
`Host` rule above deliberately keeps it working — a tunnel sends
`Host: <name>.trycloudflare.com`, which the allowlist would otherwise reject.

Direct browser access (CORS plus a remote bind) is a follow-up.
