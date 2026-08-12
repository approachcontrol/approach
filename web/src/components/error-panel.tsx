import type { ApproachApiErrorKind } from '@/lib/approach-api'

/**
 * The hint is per-kind because the fixes are unrelated: an unset environment
 * variable needs a redeploy, a stale token needs a new value, a stopped tunnel
 * needs nothing but patience. One generic "the tunnel is probably down" line
 * was actively misleading for most of these.
 */
const HINTS: Record<ApproachApiErrorKind, string> = {
  'not-configured':
    'Set APPROACH_GRAPHQL_URL in this deployment’s environment — plus APPROACH_API_TOKEN if the server was started with one — and redeploy.',
  'not-found':
    'The URL resolves, but nothing answers the API there. Check that APPROACH_GRAPHQL_URL ends in /graphql and points at the right tunnel, then redeploy.',
  unreachable:
    'The API is a local `approach serve` process reached over a tunnel, so this usually means the process is stopped or the tunnel is down. Nothing is retried automatically — reload once it is back.',
  timeout:
    'The server was still building a snapshot when the request gave up. A very large scan root can do that; reload, or narrow the server’s --scan-root.',
  unauthorized:
    'The token this deployment sends does not match the one `approach serve` was started with. Update APPROACH_API_TOKEN and redeploy.',
  forbidden:
    '`approach serve` refused the request. It only accepts loopback hosts while running without a token, so a tunnelled server needs one set.',
  busy: 'The server caps how many requests it handles at once. Reload in a moment.',
  http: 'Nothing is retried automatically — reload to try again.',
  graphql:
    'The server understood the request but could not answer it. This usually means the client and the server disagree about the schema.',
}

export function ErrorPanel({ message, kind }: { message: string; kind: ApproachApiErrorKind }) {
  return (
    <div className="panel panel--error" role="alert">
      <p className="panel__title">Could not load Approach state</p>
      <p>{message}</p>
      <p className="panel__hint">{HINTS[kind]}</p>
    </div>
  )
}

export function EmptyPanel({ children }: { children: React.ReactNode }) {
  return <div className="panel">{children}</div>
}
