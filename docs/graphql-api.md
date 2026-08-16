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

The limit walks measure **every** operation in a document, not just the one
`operationName` selects, so a document is rejected if any operation in it
exceeds a limit. Only one operation executes, so that is deliberately
conservative.

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
  handler instead enforces a `Host` allowlist (`localhost` or any loopback IP
  literal — `net.IP.IsLoopback`, so `127.0.0.2` and expanded spellings of `::1`
  count — on any port) to blunt DNS rebinding. It is deliberately the same rule
  the bind address is classified with, so no address that starts token-free
  403s every request that reaches it. Widening past `127.0.0.1` costs nothing
  against rebinding: rebinding resolves a hostname, and a rebound `Host` is a
  name, never an IP literal.
- On **any non-loopback** bind a token is required. For an IP literal or a
  wildcard host that is decided *before* the listener opens, so an
  unauthenticated listener on a non-loopback interface never exists even
  briefly. A **hostname** is only a claim about a name — `/etc/hosts` or the
  resolver decides what `localhost` means — so the resolved listener address is
  checked again after `net.Listen` and the listener is closed, unserved, if it
  turns out not to be loopback.
- The token is accepted as `Authorization: Bearer <token>` or
  `X-Approach-Token`, compared with `crypto/subtle.ConstantTimeCompare`.
- **Set the token through `APPROACH_API_TOKEN`, not `--token`.** A flag value
  lands in `argv`, readable by any local account for as long as the server
  runs, and in shell history after it stops. `--token` stays for scripts and
  one-off runs, but the missing-token error and `serve --help` both point at
  the environment variable first.
- **When a token is configured the `Host` header is not checked.** Rebinding is
  only a threat while the token is optional — a rebound browser cannot supply a
  bearer token it does not have. Checking `Host` unconditionally would 403 every
  request arriving through a tunnel, which is the recommended remote consumer
  shape below.
- `GET /healthz` is exempt from auth even when a token is configured.
- **There is no TLS.** The server speaks plaintext HTTP and has no certificate
  configuration, so a non-loopback bind puts the token — and every Flow record
  — on the wire in the clear, where any on-path observer can read the token and
  replay it. The token is an access control, not a transport control. Startup
  prints a warning to stderr on any non-loopback bind; the supported remote
  shape is the tunnel or reverse proxy below, which terminates TLS. Serving
  TLS directly is tracked as follow-up.
- **A loopback bind is machine-local, not user-local.** Any process, and any
  other OS account on the machine, can reach `127.0.0.1:8787`, send an allowed
  `Host` and `application/json`, and read repo paths, Flow instructions, phase
  notes, and issue/PR metadata — even though the artifact directory itself is
  `0700`. The `Host` and content-type rules are browser controls; they are not
  a local-user boundary. Set a token on a shared or multi-account machine.
  Requiring one by default (auto-generated, to keep zero-config `curl` working)
  is tracked as follow-up.
- **The `application/json` requirement is the CSRF control**, not just content
  negotiation. A browser form or `no-cors` `fetch` can only send the three
  CORS-safelisted content types, so requiring this one forces a preflight —
  which gets a bare 405 with no `Access-Control-*` header and never reaches the
  handler. Accepting `application/graphql` or a form encoding would open every
  local Flow record to any page the user visits while `approach serve` is up.

### Read-only is structural

No `Mutation` or `Subscription` root type exists in the schema, and the handler
rejects any parsed operation whose type is not `query` before execution.

## Hardening

| Control | Rule | Response |
| --- | --- | --- |
| Method | `POST` only on `/graphql`; `GET` only on `/healthz` | 405 |
| Unknown path | Anything other than `/graphql` and `/healthz` | 404 |
| Content-type | `application/json` required; a `charset` parameter is allowed | 415 |
| `Host` header | `localhost` or a loopback IP literal, **only when no token is configured** | 403 |
| Body size | 64 KiB | 413 |
| Query depth | Max nesting 12, measured on the parsed AST with fragments resolved | 400 |
| Introspection depth | Max nesting 20 inside a `__schema` / `__type` subtree | 400 |
| Introspection size | Max 400 field nodes inside a `__schema` / `__type` subtree | 400 |
| Query size | Max 2000 field nodes after fragment expansion | 400 |
| Fragment cycle | Any cyclic fragment spread | 400 |
| Query cost | Max 500 000 resolved values, each list field multiplied by its real cardinality in this snapshot | 400 |
| Response size | Max 16 MiB estimated on the **data path**, each scalar field charged its real width in this snapshot. Introspection is exempt and bounded separately — see below | 400 |
| In-flight cap | 8 concurrent requests, held across snapshot **and** execution | 503 |
| Request timeout | 20s around snapshot construction | 504 |
| Server timeouts | `ReadHeaderTimeout` 5s, `ReadTimeout` 15s, `WriteTimeout` 30s, `IdleTimeout` 60s | — |
| CORS | No `Access-Control-*` header is ever emitted, on any status | — |
| Response headers | `X-Content-Type-Options: nosniff`, `Cache-Control: no-store` on every response | — |

### Limits and introspection

`graphql-go/graphql` has no built-in depth or complexity limiting, so
`graphqlapi/limits.go` walks the parsed AST itself. Four denial-of-service
shapes live here, and each has exactly one control in front of it:

- A **cyclic fragment spread** (`fragment A { flows { ...B } }`,
  `fragment B { repo { ...A } }`) would recurse forever. Cycles are rejected up
  front, over the whole spread graph, before any recursive measurement.
- An **acyclic doubling chain** (`F1 { ...F2 ...F2 }`, `F2 { ...F3 ...F3 }`, …)
  is a few hundred bytes that expands to billions of fields. Per-fragment
  memoization keeps the walk linear; the saturated node count then rejects the
  document. A hard budget of 20 000 node visits is the backstop.
- **Result amplification.** `Repo.flows` ↔ `Flow.repo` is a cycle in the *type*
  graph, so
  `{ repos { flows { repo { flows { repo { flows { id } } } } } } }`
  is 102 bytes, 11 levels deep, and 11 field nodes — inside every limit above —
  yet resolves `flows³` values. Depth and node counts are linear in the query
  *text*; the result is multiplicative in the *data*, so neither can see this.
- **Meta-schema recursion**, covered under introspection below — the one shape
  the cost budgets deliberately cannot see, because introspection is exempt
  from them.

The cost walk closes the third shape. It is the only limit that runs **after**
the snapshot is built, because it is the only one that needs the data. It is
also the only one asked exclusively about documents that will really execute:
`graphql.ValidateDocument` runs first, and an invalid document skips costing
entirely and falls through to the 200-with-`errors` path. Validation fails a
document as a whole, so an invalid one resolves nothing and has no result to
bound — while the cost walk would still charge it against snapshot
cardinality, making a plain GraphQL error a transport-level 400 whose verdict
depends on how much data happens to be on disk.

The walk carries **two budgets**:

- **Values.** Each list field is multiplied by its real maximum cardinality in
  this snapshot. This bounds resolver calls and the nested maps `graphql-go`
  builds for the result, both of which are per-value however wide each value is.
- **Bytes.** Each scalar field is charged its real maximum width in this
  snapshot. Value count alone is a bad proxy for response size: `Flow
  .instructions` and `Phase.notes` are unbounded agent-supplied text, so a
  query resolving only a few thousand values can still serialize to hundreds of
  megabytes — and `encoding/json` builds the whole body in one buffer before a
  byte reaches the socket.

Both come from `snapshot.bounds()`, so both track the data: the same query is
served against a small snapshot and rejected against a large one. Three details
are load-bearing, and each was a hole before it was closed:

- Widths are the **encoded** width, not `len()`. `encoding/json` expands `<`,
  `>`, `&` and every control byte to a six-byte `\uXXXX`, and those are
  ordinary characters in a Flow's markdown — measuring `len()` would put the
  real ceiling at six times the documented one.
- The byte charge for a field's key uses its **alias** when it has one. An
  alias is client-chosen and unbounded, so charging the field name would leave
  a 55 KiB alias under a list traversal free.
- **One resolver call and one response key are charged per parent object,
  outside the cardinality multiplier**; only elements and their subtrees scale
  with it. A list field runs its resolver once and writes its key once whether
  the list holds zero elements or a thousand, so scaling those made the whole
  field free wherever the list is empty in this snapshot — two holes at once.
  `"<55 KiB alias>":[]` across 400 repos is a 22 MB response from a request
  that fits the 64 KiB body cap, and 999 aliases of `flows { id }` across 800
  repos runs `Repo.flows` 799 200 times; both were assessed at nearly nothing.
- **A selection the schema cannot execute is charged nothing at all** — a field
  the parent type does not define, or a selection set hung off a leaf. Either
  fails GraphQL validation for the whole document, so nothing executes and
  there is no response to bound. Charging it and then multiplying by the
  enclosing list turned typos into transport-level 400s on a large store:
  `{ repos { typo } }` was "response too large" past ~2 000 repos, and 1 000
  aliases of an unknown field was "too many values" past ~500 — both against
  the 200-with-`errors` contract. The pessimistic fallback below is for a
  *schema* field nobody measured, not for a field that does not exist.
- **Encoded widths are counted, not produced.** `jsonStringBytes` reimplements
  `encoding/json`'s escaping rules rather than calling `json.Marshal` and
  measuring the result. `snapshot.bounds()` measures every stored string on
  every request, whether or not the query selects it, so marshalling to measure
  allocated an escaped copy — up to six times the value — of unbounded
  `Flow.instructions` and `Phase.notes`, before any budget had been consulted,
  across up to 8 requests at once. `TestJSONStringBytesMatchesTheEncoder` sweeps
  every byte and every escape class to keep the two exactly equal, since a
  width that drifts *below* the encoder's is the undercount this budget exists
  to prevent.
- **`__typename` is not exempt from the cost limit**, unlike `__schema` and
  `__type`. It resolves once per snapshot object rather than against the fixed
  schema, so it is a data-proportional leaf that happens to start with `__`.
- `Flow.phases` is measured through `flowstore.OrderedPhases`, not
  `len(Record.Phases)`, because a record whose rows share a phase id expands
  there.

The fallbacks for an unmapped field are deliberately pessimistic, since
under-counting is the one mistake that reopens the hole;
`TestResultBoundsCoverEveryField` fails if a field is added to the schema
without a cardinality bound or a measured width.

**Known limitation: the byte bound is `max width × cardinality`, not the sum.**
A store whose text is heavily skewed — one Flow with a very large
`instructions`, the rest small — is charged as though every Flow were the
largest, so `{ flows { instructions } }` can be rejected with a 400 even though
the real response would be well inside the budget. It fails closed, and
narrowing the query (`flow(id:)`, or selecting fewer text fields) works around
it. `repos` and `flows` take no `first`/`after` arguments yet; pagination and a
population-total bound are the follow-up that removes this.

Subtrees rooted at a field whose name starts with `__` leave the ordinary depth
limit and enter `maxIntrospectionDepth` (20) instead, so client codegen keeps
working — the canonical `IntrospectionQuery` measures 13, deeper than any data
query has cause to be. `__schema` and `__type` are additionally **exempt from
the cost limits**, because they resolve against the schema, which is small and
fixed, rather than against the snapshot. `__typename` gets the depth treatment
but **not** the cost exemption: it is a leaf, so there is no nesting to relax,
and it resolves once per snapshot object.

**Introspection is limited, not exempt, and both of its limits are
load-bearing.** The meta-schema is itself recursive — `__Type.fields` →
`__Field.type` → `__Type` — and because `__schema` / `__type` are exempt from
the cost budgets, those two limits are the *only* controls in front of it.

- **Depth.** `fields { type { ofType { ofType {` repeats a four-level cycle
  that doubles the response each time. Depth-exempt, that is an exponential
  blowup from a sub-1 KB request; the ordinary node cap does not catch it
  either, since the response passes 16 MiB at 60 field nodes — 3% of that cap.
- **Node count.** Depth alone is not enough: alias fan-out across the
  meta-schema's own lists amplifies *within* the depth cap. At the ordinary
  2000-node cap a 34 KiB request returned 23 MiB, past the response ceiling.

The canonical `IntrospectionQuery` measures depth 13 and 181 nodes, so client
codegen keeps working with headroom under both.

**The 16 MiB response budget is a data-path budget, not a whole-response
guarantee.** Because `inspectCost` charges `__schema` / `__type` nothing,
introspection bytes are *additive on top of it*: a document that pairs a
budget-filling data query with a wide introspection selection can return around
30 MiB, and introspection alone has been measured at 14 MiB. Both figures are
adversarial shapes found by search rather than analytic bounds, so treat them
as floors, not ceilings — and note that schema growth (more types, more fields,
longer descriptions) raises them with no limit change.

This is bounded, not open-ended: the in-flight semaphore caps concurrency at 8,
and no request shape found so far kills or wedges the process. The fix is to
charge introspection into the same budget rather than exempting it — either a
per-node byte charge or real cardinalities for the meta-schema's lists — so
that `exceedsBudget` sees one number. That is the follow-up, together with
pagination.

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
through to execution and gets the 200-with-`errors` response. A *validation*
failure is not a 400 either — the cost and response-size limits run only over
a document `graphql.ValidateDocument` accepts, so an unknown field, a missing
sub-selection, or a bad argument gets the same 200 no matter how large the
store is. Only depth, node-count, fragment-cycle, cost, and response-size
rejections are 400s; the first three are structural and apply to any document
that parses, the last two only to one that would really execute.

### Errors and logs

Store and scan failures are logged server-side with full detail. A non-partial
Flow-store failure surfaces to clients as the fixed message
`internal error reading application state` — never a filesystem path or `os`
error text. A typed partial Flow-list result is different: the server logs the
full skipped-row diagnostic, indexes the accompanying healthy records, and
serves them normally. The GraphQL schema has no degradation field, and skipped
row IDs or decoder causes never enter a response.

One log line per request: method, path, status, duration. Never the
`Authorization` or `X-Approach-Token` value, and never the query body. Request
logs go to stderr; only the resolved listen address goes to stdout.

The path is logged **quoted, percent-encoded, and capped at 128 bytes**, not as
`url.URL.Path`. That field is percent-*decoded*, and 404s are logged before any
auth or `Host` check runs, so logging it raw would let an unauthenticated
`GET /x%0aapproach graphql: POST /graphql 200 1ms` forge a whole log line — or
push terminal escapes into a foreground stderr.

## Live state and consistency

Each request builds **one snapshot** — one `scanner.Scan`, one `flowstore.List`,
and one `ReadEpicProgression` per distinct epic those Flows name — and resolves
the whole query from it. A single response is therefore internally consistent,
and every new request re-reads disk. There is no cross-request cache.

All three reads happen before execution and none of them depends on the query:
the response-size limiter has to bound what the snapshot *could* serialize, not
what this query selected. The progression reads are indexed point lookups on the
already-open store, so the `scanner.Scan` filesystem walk dominates.

**A failing scan degrades; a non-partial failing store errors.** A missing or
mistyped scan root is the ordinary case, not an exceptional one, so a scan
failure yields an empty scanned-repo set plus one logged warning: flow-derived
repos still populate `repos`, and `flows` / `flow` are unaffected. When
`flowstore.List` returns healthy rows with its typed partial diagnostic, the
snapshot logs the diagnostic and indexes those rows. Every other Flow-store
failure is a real failure of the primary data source and surfaces as the
sanitized GraphQL error above.

**The scan cannot be cancelled.** `scanner.Scan`, `flowstore.List`, and
`ReadEpicProgression` take no `context.Context` and read synchronously, so the
request timeout cannot interrupt them. The design is explicit about this:

1. The handler takes an in-flight semaphore slot *before* launching snapshot
   construction; if none is free it returns 503 immediately.
2. Snapshot construction runs in a goroutine; the handler waits on it against
   the timeout.
3. On timeout the handler returns 504 **while the slot is still held**. A
   watcher goroutine releases it when the scan finally finishes, never the
   handler. Releasing on handler return would let a client that fires and
   abandons requests spawn unbounded concurrent orphaned scans, with 503 never
   firing — the two controls would cancel each other out.
4. On success the handler **keeps the slot across execution** and releases it
   on return. Execution is where the cost is, so a slot that covered only the
   snapshot would cap the cheap phase and leave the expensive one unbounded.
5. Resolvers then run against the completed in-memory snapshot and are pure map
   and slice reads, over a result the cost limit has already bounded.

The slot is never released while work it accounts for is still running, so the
semaphore is the real backpressure control across the whole request; the
timeout only bounds how long a client waits. `[scan].max_depth` bounds each
scan.

**Execution does not inherit the request's cancellation.** `graphql.Do` returns
when its context is cancelled but leaves its own execution goroutine running to
completion, so honoring a client disconnect would build up orphaned executions
that no semaphore slot accounts for — the same failure mode point 3 prevents
for scans. Execution therefore runs against `context.WithoutCancel`. Nothing
bounds execution by wall clock, so this is safe precisely because the two cost
budgets have already bounded the work — they are load-bearing for that
decision, not just for response size.

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
  bead: BeadLink
  epicProgression: EpicProgression
  issue: Issue
  pullRequest: PullRequest
  merge: Merge
  phases: [Phase!]!
  currentPhase: Phase
  createdAt: DateTime!
  updatedAt: DateTime!
}

type BeadLink {
  id: ID!
  epicId: ID
}

type EpicProgression {
  enabled: Boolean!
  done: Boolean!
  halt: EpicProgressionHalt
}

type EpicProgressionHalt {
  childBeadId: ID!
  status: String!
  message: String!
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
| `Issue.number`, `PullRequest.number` | the number is unset (`0` is not emitted) |
| `Flow.bead` | the Flow links no Bead |
| `BeadLink.epicId` | the link records a child Bead but no epic |
| `Flow.epicProgression` | the Flow links no epic, **or** that epic has no progression row |
| `EpicProgression.halt` | progression has not halted |

`Phase.dependsOn` is `[String!]!` and is **never** null: a phase with no edges
serializes as `[]`.

### Bead linkage and epic progression

`Flow.bead` is the Beads issue the Flow tracks, and `Flow.epicProgression` is
the durable auto-progression state of that Bead's epic (`docs/flow-phases.md`
covers what progression does). Both are read-only projections of what the store
already holds — **the API never reads Beads itself and never writes progression
state**; the server is wired to `flowstore.Store.ReadEpicProgression` and to
nothing else.

- `BeadLink.id` and `BeadLink.epicId` are the persisted link text, **verbatim**.
  A child-only link resolves a non-null `bead` with `epicId: null`.
- Progression is looked up by canonical `(normalized repo path, trimmed epic
  id)`, compatible with what `ReadEpicProgression` canonicalizes, so a padded or
  differently spelled epic id still resolves the right row while `bead.epicId`
  keeps reading back exactly as stored.
- `epicProgression` is null whenever `bead` is. An epic id with no child Bead id
  is a shape the store forbids, so only an externally written record holds one;
  the two fields never disagree about whether a Flow is linked.
- A **missing row is `null`, not a disabled epic.** Nothing is synthesized:
  "nobody enabled progression for this epic" and "somebody turned it off" are
  different claims, and only the second one has a row.
- `done` is the persisted completion flag. It is never derived from
  `enabled: false` — a manually disabled epic is not finished.
- `halt` carries the child Bead, the child Flow status that stopped
  progression (`blocked`, `needs_attention`, `closed`, or `abandoned`), and a
  free-text message.
- One request reads **one row per distinct canonical key**. Sibling Flows under
  the same epic share a single read, found or missing, and a Flow that links no
  epic causes none. The reads are eager and do not depend on the query — the
  response-size limiter has to know the widest halt message before executing —
  so a request costs one indexed point lookup per distinct epic your Flows
  name, whether or not it selects `epicProgression`.
- A row that cannot be read — a malformed record is likelier here than I/O —
  is **scoped to the epic that failed**. `Flow.epicProgression` resolves null
  for the Flows naming that epic and reports the fixed `internal error reading
  application state` message in the `errors` array; the key and the cause go to
  the server log only. Every other field, every other Flow, and any query that
  does not select `epicProgression` are unaffected. This is deliberately weaker
  than the Flow-list failure, which fails the request: progression is an
  optional projection and must not be a single point of failure for the API.
  Because that error repeats per Flow and per aliased selection, the
  response-size limit charges each of those entries — including the response
  keys its `path` names — so an unreadable row cannot amplify past the cap. A
  snapshot with nothing unreadable in it is charged nothing.

```graphql
{
  flow(id: "20260814T210915Z-surface-bead-link") {
    bead { id epicId }
    epicProgression {
      enabled
      done
      halt { childBeadId status message }
    }
  }
}
```

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
`Host: <name>.trycloudflare.com` or `Host: <machine>.<tailnet>.ts.net`, which
the allowlist would otherwise reject.

`web/` is that consumer: a Next.js app, deployed on Vercel, whose GraphQL calls
all run in React Server Components so the endpoint and token stay server-side.
It reaches this API over a tunnel and renders repositories, Flows, and phases.
Setup, tunnel, and deploy steps are `web/README.md`.

Direct browser access (CORS plus a remote bind) is a follow-up.

### Tunnels and the token

Any tunnel — Cloudflare, Tailscale Funnel, an nginx/Caddy reverse proxy —
leaves the bind on `127.0.0.1` and reaches the network on the server's behalf.
So the bind classification never demands a token, and it is easy to conclude
none is needed. **Configure one anyway**: without it the handler enforces the
`Host` allowlist, and the tunnel's `Host` is not `localhost` or a loopback IP
literal, so every request 403s. The token is what turns that check off, and it
is also the only access control on a URL that is now publicly reachable.

Tailscale Funnel is the recommended shape for `web/`, and it differs from the
others in one way that matters to this server's threat model. Funnel relays TLS
with SNI passthrough to a certificate issued for the node, so TLS terminates on
the machine running `approach serve`. A Cloudflare tunnel or a hosted proxy
terminates TLS on someone else's hardware, which — given this server speaks
plaintext with no TLS of its own — puts the bearer token and every Flow record
in the clear there.

Funnel is still *public* ingress: tailnet ACLs do not apply to it, unlike
`tailscale serve`. Anyone with the `ts.net` hostname reaches `/graphql` gated
only by the token, and `/healthz` gated by nothing.
