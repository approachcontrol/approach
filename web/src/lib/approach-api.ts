import 'server-only'

/**
 * Typed client for the Approach read-only GraphQL API (`docs/graphql-api.md`).
 *
 * Every call runs server-side: the endpoint and the bearer token live in
 * server environment variables and are never shipped to the browser, which is
 * the shape the API's own "Consuming from a web app" guidance recommends.
 */

/** The API bounds snapshot construction at 20s; abort after that, not before. */
const REQUEST_TIMEOUT_MS = 25_000

export type ApproachApiErrorKind =
  | 'not-configured'
  | 'unreachable'
  | 'timeout'
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
  unreachable: 'Could not reach the Approach API.',
  timeout: 'The Approach API did not respond in time.',
  http: 'The Approach API returned an unexpected response.',
  graphql: 'The Approach API could not answer that query.',
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
    const name = caught instanceof Error ? caught.name : ''
    if (name === 'TimeoutError' || name === 'AbortError') {
      fail('timeout', caught)
    }
    fail('unreachable', caught)
  }

  let body: GraphQLResponse<T>
  try {
    body = (await response.json()) as GraphQLResponse<T>
  } catch (caught) {
    // A tunnel or proxy error page arrives as HTML, including on a 200.
    fail('http', `status ${response.status}: ${caught}`)
  }

  if (!response.ok) {
    // Transport, auth, and limit failures. Messages are fixed server-side
    // strings, but they still describe the deployment, so they only get logged.
    fail('http', `status ${response.status}: ${JSON.stringify(body.errors ?? null)}`)
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

export async function getRepos(): Promise<RepoSummary[]> {
  const data = await query<{ repos: RepoSummary[] }>(REPOS_QUERY)
  return data.repos
}

export async function getRepo(id: string): Promise<RepoDetail | null> {
  const data = await query<{ repo: RepoDetail | null }>(REPO_QUERY, { id })
  return data.repo
}

export async function getFlow(id: string): Promise<FlowDetail | null> {
  const data = await query<{ flow: FlowDetail | null }>(FLOW_QUERY, { id })
  return data.flow
}
