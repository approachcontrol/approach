import 'server-only'

/**
 * Typed client for the Approach read-only GraphQL API (`docs/graphql-api.md`).
 *
 * Every call runs server-side: the endpoint and the bearer token live in
 * server environment variables and are never shipped to the browser, which is
 * the shape the API's own "Consuming from a web app" guidance recommends.
 */

/**
 * Backstop only. The API bounds snapshot construction at 20s and answers 504
 * itself, so a slow scan is normally a response, not an abort; this catches the
 * case where no response arrives at all — a hung tunnel or a dropped
 * connection — before the platform's own function timeout does.
 */
const REQUEST_TIMEOUT_MS = 25_000

export type ApproachApiErrorKind =
  | 'not-configured'
  | 'not-found'
  | 'unreachable'
  | 'timeout'
  | 'unauthorized'
  | 'forbidden'
  | 'busy'
  | 'http'
  | 'graphql'

/**
 * Carries a fixed, safe message. The endpoint URL, the token, and the
 * underlying error text stay server-side in the log line, because this message
 * reaches the browser through `error.tsx`.
 */
export class ApproachApiError extends Error {
  readonly kind: ApproachApiErrorKind

  constructor(kind: ApproachApiErrorKind, message: string) {
    super(message)
    this.name = 'ApproachApiError'
    this.kind = kind
  }
}

const MESSAGES: Record<ApproachApiErrorKind, string> = {
  'not-configured': 'The Approach API endpoint is not configured for this deployment.',
  'not-found': 'Nothing is serving the Approach API at the configured URL.',
  unreachable: 'Could not reach the Approach API.',
  timeout: 'The Approach API did not respond in time.',
  unauthorized: 'The Approach API rejected this deployment’s token.',
  forbidden: 'The Approach API refused a request from this deployment.',
  busy: 'The Approach API is at its concurrent-request limit.',
  http: 'The Approach API returned an unexpected response.',
  graphql: 'The Approach API could not answer that query.',
}

/**
 * The API's own status contract is a table in `docs/graphql-api.md` under
 * "Hardening". Collapsing it all into one message made a stale token and a dead
 * tunnel look identical, and hid the 504 the server raises at its own 20s
 * snapshot bound — which fires well before this client's abort, so it is the
 * timeout path that actually happens in production.
 *
 * The tunnel in front of the API has a contract too, and it is the more likely
 * thing to be broken: the deploy story requires one, and it answers for the API
 * whenever `approach serve` is not running. Those statuses map to `unreachable`
 * so the panel names the tunnel rather than shrugging.
 */
function kindForStatus(status: number): ApproachApiErrorKind {
  switch (status) {
    case 401:
      return 'unauthorized'
    case 403:
      return 'forbidden'
    case 404:
      // The API 404s only on an unknown path, so this is the URL, not the data.
      return 'not-found'
    case 503:
      return 'busy'
    case 504:
      return 'timeout'
    // 502 is a generic bad gateway; 521-530 are Cloudflare's origin-side
    // failures, which is what a stopped `approach serve` behind `cloudflared`
    // actually produces.
    case 502:
    case 521:
    case 522:
    case 523:
    case 530:
      return 'unreachable'
    default:
      // 400/405/413/415 are all "this client sent something wrong" — a bug
      // here, not a condition the reader can act on.
      return 'http'
  }
}

// `AbortSignal.timeout` rejects with a DOMException named `TimeoutError`; an
// explicit `controller.abort()` produces `AbortError`. Either way the deadline
// fired, which says nothing about whatever status may already have arrived.
function isAbort(caught: unknown): boolean {
  const name = caught instanceof Error ? caught.name : ''
  return name === 'TimeoutError' || name === 'AbortError'
}

function fail(kind: ApproachApiErrorKind, detail: unknown): never {
  // Server-side only: `detail` may name the endpoint host.
  console.error(`approach-api ${kind}:`, detail)
  throw new ApproachApiError(kind, MESSAGES[kind])
}

function endpoint(): string {
  const url = process.env.APPROACH_GRAPHQL_URL?.trim()
  if (!url) {
    fail('not-configured', 'APPROACH_GRAPHQL_URL is unset')
  }
  return url
}

function authHeaders(): Record<string, string> {
  const token = process.env.APPROACH_API_TOKEN?.trim()
  // A loopback server without a token is a supported local-dev shape, so an
  // absent token is not an error — it just means no header.
  return token ? { authorization: `Bearer ${token}` } : {}
}

interface GraphQLResponse<T> {
  data?: T | null
  errors?: { message?: string }[]
}

async function query<T>(document: string, variables?: Record<string, unknown>): Promise<T> {
  const url = endpoint()

  let response: Response
  try {
    response = await fetch(url, {
      method: 'POST',
      // `application/json` is the API's CSRF control, not just negotiation.
      headers: { 'content-type': 'application/json', ...authHeaders() },
      body: JSON.stringify({ query: document, variables }),
      cache: 'no-store',
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    })
  } catch (caught) {
    if (isAbort(caught)) {
      fail('timeout', caught)
    }
    fail('unreachable', caught)
  }

  let parsed: unknown
  try {
    parsed = await response.json()
  } catch (caught) {
    // The deadline is still armed once the headers land, so a tunnel that
    // answers 200 and then stalls mid-body aborts *here*, not above. That is
    // precisely the hung tunnel this client's own abort exists to catch, and
    // classifying it by the status already in hand would render the generic
    // panel for a 200 instead of the timeout one.
    if (isAbort(caught)) {
      fail('timeout', caught)
    }
    // A tunnel or proxy error page arrives as HTML, including on a 200. The
    // status still classifies it: a 504 from the API and a 504 from the tunnel
    // mean the same thing to the reader.
    fail(kindForStatus(response.status), `status ${response.status}: unparseable body: ${caught}`)
  }

  // A successful parse is not an object: JSON's grammar admits bare `null`,
  // numbers, and strings, and a proxy that answers `null` is parseable. Reading
  // `.errors` off it would throw a TypeError, and `load()` rethrows anything
  // that is not an ApproachApiError — so the reader would get the generic
  // boundary instead of the panel this whole module exists to reach.
  if (typeof parsed !== 'object' || parsed === null) {
    fail(
      kindForStatus(response.status),
      `status ${response.status}: non-object body: ${JSON.stringify(parsed) ?? typeof parsed}`,
    )
  }
  const body = parsed as GraphQLResponse<T>

  if (!response.ok) {
    // Transport, auth, and limit failures. The server's own messages are fixed
    // strings, but they still describe the deployment, so they only get logged.
    fail(
      kindForStatus(response.status),
      `status ${response.status}: ${JSON.stringify(body.errors ?? null)}`,
    )
  }
  if (body.errors?.length) {
    fail('graphql', JSON.stringify(body.errors))
  }
  if (body.data == null) {
    fail('http', 'response carried neither data nor errors')
  }
  return body.data
}

export interface FlowRef {
  id: string
}

export interface RepoSummary {
  id: string
  path: string
  displayName: string
  isBare: boolean
  inScanRoot: boolean
  flows: FlowRef[]
}

export interface PhaseRef {
  id: string
  title: string
  kind: string
  status: string
}

export interface FlowSummary {
  id: string
  title: string
  status: string
  autoMode: boolean
  branch: string | null
  updatedAt: string
  currentPhase: PhaseRef | null
}

export interface RepoDetail {
  id: string
  path: string
  displayName: string
  isBare: boolean
  inScanRoot: boolean
  flows: FlowSummary[]
}

export interface Phase {
  id: string
  parentPhaseId: string | null
  title: string
  kind: string
  status: string
  order: number
  dependsOn: string[]
  outcome: string | null
  summary: string | null
  createdAt: string
  updatedAt: string
}

export interface Issue {
  provider: string | null
  number: number | null
  url: string | null
}

export interface PullRequest {
  provider: string | null
  number: number | null
  url: string | null
  headBranch: string | null
  baseBranch: string | null
  status: string | null
}

export interface Merge {
  status: string | null
  commit: string | null
  mergedAt: string | null
}

export interface BeadLink {
  id: string
  epicId: string | null
}

export interface EpicProgressionHalt {
  childBeadId: string
  status: string
  message: string
}

export interface EpicProgression {
  enabled: boolean
  done: boolean
  halt: EpicProgressionHalt | null
}

export interface FlowDetail {
  id: string
  title: string
  status: string
  repoPath: string
  repo: { id: string; displayName: string }
  worktreePath: string | null
  branch: string | null
  baseRef: string | null
  commit: string | null
  presetName: string | null
  planId: string | null
  autoMode: boolean
  bead: BeadLink | null
  // Null both when the Flow links no epic and when that epic has no persisted
  // progression row; the two are distinguished by `bead.epicId`.
  epicProgression: EpicProgression | null
  issue: Issue | null
  pullRequest: PullRequest | null
  merge: Merge | null
  phases: Phase[]
  currentPhase: { id: string } | null
  createdAt: string
  updatedAt: string
}

// `Flow.instructions` and `Phase.notes` are deliberately never selected: they
// are unbounded agent-authored text, and a deployed instance republishes
// whatever it renders.

const REPOS_QUERY = `query Repos {
  repos { id path displayName isBare inScanRoot flows { id } }
}`

const REPO_QUERY = `query Repo($id: ID!) {
  repo(id: $id) {
    id
    path
    displayName
    isBare
    inScanRoot
    flows {
      id
      title
      status
      autoMode
      branch
      updatedAt
      currentPhase { id title kind status }
    }
  }
}`

const FLOW_QUERY = `query Flow($id: ID!) {
  flow(id: $id) {
    id
    title
    status
    repoPath
    repo { id displayName }
    worktreePath
    branch
    baseRef
    commit
    presetName
    planId
    autoMode
    bead { id epicId }
    epicProgression { enabled done halt { childBeadId status message } }
    issue { provider number url }
    pullRequest { provider number url headBranch baseBranch status }
    merge { status commit mergedAt }
    phases {
      id
      parentPhaseId
      title
      kind
      status
      order
      dependsOn
      outcome
      summary
      createdAt
      updatedAt
    }
    currentPhase { id }
    createdAt
    updatedAt
  }
}`

/**
 * `data` being non-null does not make it the shape the operation asked for.
 * `{"data":{}}` is a perfectly good GraphQL envelope, and a stale tunnel URL
 * that now resolves to some other JSON service can produce one — the README
 * warns that quick tunnels change URL on every run. Without this check the
 * selection comes back `undefined`, and the page throws on `.length` or
 * `.phases`, landing in the generic boundary instead of the sanitized panel.
 *
 * These check the top-level selection only: present, and the right broad shape.
 * Validating every field would mean a schema validator and a dependency, and
 * the real defence against field-level drift is already
 * `graphqlapi/web_documents_test.go`, which holds these documents to the live
 * schema at `make test` time.
 */
function requireList<T>(value: unknown, operation: string): T[] {
  if (!Array.isArray(value)) {
    fail('http', `${operation} returned ${describe(value)} where a list was selected`)
  }
  return value as T[]
}

function requireNodeOrNull<T>(value: unknown, operation: string): T | null {
  if (value === null) {
    return null
  }
  // `typeof [] === 'object'`, so the array case needs saying out loud: a list
  // here would sail past and then throw on `.flows` or `.phases`.
  if (typeof value !== 'object' || Array.isArray(value)) {
    fail('http', `${operation} returned ${describe(value)} where an object or null was selected`)
  }
  return value as T
}

function describe(value: unknown): string {
  return value === undefined ? 'no such field' : `a ${Array.isArray(value) ? 'list' : typeof value}`
}

export async function getRepos(): Promise<RepoSummary[]> {
  const data = await query<{ repos: RepoSummary[] }>(REPOS_QUERY)
  return requireList<RepoSummary>(data.repos, 'repos')
}

export async function getRepo(id: string): Promise<RepoDetail | null> {
  const data = await query<{ repo: RepoDetail | null }>(REPO_QUERY, { id })
  return requireNodeOrNull<RepoDetail>(data.repo, 'repo')
}

export async function getFlow(id: string): Promise<FlowDetail | null> {
  const data = await query<{ flow: FlowDetail | null }>(FLOW_QUERY, { id })
  return requireNodeOrNull<FlowDetail>(data.flow, 'flow')
}
