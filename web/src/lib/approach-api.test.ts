import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApproachApiError, getFlow, getRepo, getRepos } from './approach-api'

const ENDPOINT = 'https://tunnel.example/graphql'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

function lastRequest(fetchMock: ReturnType<typeof vi.fn>) {
  const [url, init] = fetchMock.mock.calls.at(-1) as [string, RequestInit]
  return {
    url,
    init,
    headers: new Headers(init.headers),
    body: JSON.parse(String(init.body)) as {
      query: string
      variables?: Record<string, unknown>
    },
  }
}

describe('approach api client', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    vi.stubEnv('APPROACH_GRAPHQL_URL', ENDPOINT)
    vi.stubEnv('APPROACH_API_TOKEN', '')
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  describe('request shape', () => {
    it('POSTs JSON to the configured endpoint without caching', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { repos: [] } }))

      await getRepos()

      const req = lastRequest(fetchMock)
      expect(req.url).toBe(ENDPOINT)
      expect(req.init.method).toBe('POST')
      expect(req.headers.get('content-type')).toBe('application/json')
      expect(req.init.cache).toBe('no-store')
      expect(req.body.query).toContain('repos')
    })

    it('omits the Authorization header when no token is configured', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { repos: [] } }))

      await getRepos()

      expect(lastRequest(fetchMock).headers.get('authorization')).toBeNull()
    })

    it('sends a bearer token when one is configured', async () => {
      vi.stubEnv('APPROACH_API_TOKEN', 's3cret')
      fetchMock.mockResolvedValue(jsonResponse({ data: { repos: [] } }))

      await getRepos()

      expect(lastRequest(fetchMock).headers.get('authorization')).toBe('Bearer s3cret')
    })

    it('passes ids through as GraphQL variables, never string-interpolated', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { repo: null } }))

      await getRepo('/Users/brian/dev/approach')

      const req = lastRequest(fetchMock)
      expect(req.body.variables).toEqual({ id: '/Users/brian/dev/approach' })
      expect(req.body.query).not.toContain('/Users/brian')
    })

    it('aborts before the server-side timeout instead of hanging', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { repos: [] } }))

      await getRepos()

      expect(lastRequest(fetchMock).init.signal).toBeInstanceOf(AbortSignal)
    })
  })

  describe('configuration', () => {
    it('fails with a not-configured error when the endpoint is unset', async () => {
      vi.stubEnv('APPROACH_GRAPHQL_URL', '')

      await expect(getRepos()).rejects.toMatchObject({ kind: 'not-configured' })
      expect(fetchMock).not.toHaveBeenCalled()
    })
  })

  describe('error mapping', () => {
    it('maps a non-200 response to an http error', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ errors: [{ message: 'request too large' }] }, 413),
      )

      await expect(getRepos()).rejects.toMatchObject({ kind: 'http' })
    })

    // The status table is documented under "Hardening" in docs/graphql-api.md.
    // Collapsing these into one kind made a stale token and a stopped tunnel
    // indistinguishable to the reader.
    it.each([
      [401, 'unauthorized'],
      [403, 'forbidden'],
      [404, 'not-found'],
      [503, 'busy'],
      [504, 'timeout'],
      [400, 'http'],
      [405, 'http'],
      [413, 'http'],
      [415, 'http'],
      // The tunnel answers for the API when `approach serve` is not running.
      // 521-530 are Cloudflare's origin-side failures.
      [502, 'unreachable'],
      [521, 'unreachable'],
      [522, 'unreachable'],
      [523, 'unreachable'],
      [530, 'unreachable'],
    ])('maps status %i to the %s kind', async (status, kind) => {
      fetchMock.mockResolvedValue(jsonResponse({ errors: [{ message: 'nope' }] }, status))

      await expect(getRepos()).rejects.toMatchObject({ kind })
    })

    // The server bounds snapshot construction at 20s and answers 504 itself,
    // which fires before this client's 25s abort — so 504 is the timeout path
    // that actually happens in production, not the abort.
    it('reports the API’s own 20s snapshot timeout as a timeout, not a generic failure', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ errors: [{ message: 'request timed out' }] }, 504),
      )

      const error = await getRepos().catch((caught: unknown) => caught)

      expect(error).toMatchObject({ kind: 'timeout' })
      expect((error as Error).message).toBe('The Approach API did not respond in time.')
    })

    it('classifies an unparseable body by status too', async () => {
      fetchMock.mockResolvedValue(new Response('<html>gateway timeout</html>', { status: 504 }))

      await expect(getRepos()).rejects.toMatchObject({ kind: 'timeout' })
    })

    it('maps a 200 carrying a GraphQL errors array to a graphql error', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ data: null, errors: [{ message: 'internal error reading application state' }] }),
      )

      await expect(getRepos()).rejects.toMatchObject({ kind: 'graphql' })
    })

    it('maps a network failure to an unreachable error', async () => {
      fetchMock.mockRejectedValue(new TypeError('fetch failed'))

      await expect(getRepos()).rejects.toMatchObject({ kind: 'unreachable' })
    })

    it('maps an abort to a timeout error', async () => {
      fetchMock.mockRejectedValue(
        Object.assign(new Error('The operation was aborted'), { name: 'TimeoutError' }),
      )

      await expect(getRepos()).rejects.toMatchObject({ kind: 'timeout' })
    })

    it('maps an unparseable body to an http error', async () => {
      fetchMock.mockResolvedValue(new Response('<html>gateway</html>', { status: 200 }))

      await expect(getRepos()).rejects.toMatchObject({ kind: 'http' })
    })

    it('never leaks the endpoint or the token in an error message', async () => {
      vi.stubEnv('APPROACH_API_TOKEN', 's3cret')
      fetchMock.mockRejectedValue(new TypeError(`fetch failed for ${ENDPOINT}`))

      const error = await getRepos().catch((caught: unknown) => caught)

      expect(error).toBeInstanceOf(ApproachApiError)
      const message = (error as Error).message
      expect(message).not.toContain('tunnel.example')
      expect(message).not.toContain('s3cret')
    })
  })

  describe('result mapping', () => {
    it('returns the repo list', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          data: {
            repos: [
              {
                id: '/dev/a',
                path: '/dev/a',
                displayName: 'a',
                isBare: false,
                inScanRoot: true,
                flows: [{ id: 'f1' }],
              },
            ],
          },
        }),
      )

      await expect(getRepos()).resolves.toEqual([
        {
          id: '/dev/a',
          path: '/dev/a',
          displayName: 'a',
          isBare: false,
          inScanRoot: true,
          flows: [{ id: 'f1' }],
        },
      ])
    })

    it('returns null for an unknown repo id', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { repo: null } }))

      await expect(getRepo('/nope')).resolves.toBeNull()
    })

    it('returns null for an unknown flow id', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { flow: null } }))

      await expect(getFlow('nope')).resolves.toBeNull()
    })

    it('never selects instructions or phase notes', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { flow: null } }))

      await getFlow('f1')

      const { query } = lastRequest(fetchMock).body
      expect(query).not.toContain('instructions')
      expect(query).not.toContain('notes')
    })
  })
})
