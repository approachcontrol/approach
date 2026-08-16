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

    // The API scopes some failures to one field: an unreadable epic progression
    // row nulls `Flow.epicProgression` for the Flows naming that epic and leaves
    // the rest of the response intact. Discarding the data would turn a missing
    // optional field into a blank error page for the whole Flow.
    it('keeps a partial response whose errors all name a tolerated field', async () => {
      const errors = [
        {
          message: 'internal error reading application state',
          path: ['flow', 'epicProgression'],
        },
      ]
      fetchMock.mockResolvedValue(
        jsonResponse({
          data: { flow: { id: 'f1', bead: { id: 'approach-y7g.7', epicId: 'approach-y7g' }, epicProgression: null } },
          errors,
        }),
      )
      const logged = vi.spyOn(console, 'error').mockImplementation(() => {})

      const flow = await getFlow('f1')

      expect(flow?.id).toBe('f1')
      expect(flow?.epicProgression).toBeNull()
      // The null is the same one a missing row produces, so the failure has to
      // survive as its own flag or the viewer reports "not configured".
      expect(flow?.epicProgressionUnavailable).toBe(true)
      // Not silent: the reader sees the Flow, the operator sees the cause.
      expect(logged).toHaveBeenCalledWith('approach-api partial response:', JSON.stringify(errors))
      logged.mockRestore()
    })

    it('leaves the progression flag clear when nothing failed', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ data: { flow: { id: 'f1', bead: null, epicProgression: null } } }),
      )

      expect((await getFlow('f1'))?.epicProgressionUnavailable).toBe(false)
    })

    // The regression the whitelist exists for. A failed snapshot errors the
    // `flow` root, and GraphQL nulls a failed field up to its nearest nullable
    // parent — so the body is byte-identical to an unknown id apart from the
    // errors array. Tolerating any response that carries `data` rendered "flow
    // not found" for a store the server could not read at all.
    it('rejects an errored null root rather than reporting it as a missing flow', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          data: { flow: null },
          errors: [{ message: 'internal error reading application state', path: ['flow'] }],
        }),
      )

      await expect(getFlow('f1')).rejects.toMatchObject({ kind: 'graphql' })
    })

    // The response body is a cast, not a parsed shape. A TypeError raised while
    // reading it would escape the ApproachApiError path into the generic error
    // boundary — the outcome the non-object-body check exists to prevent.
    it.each([
      ['a non-list errors field', { data: { flow: null }, errors: {} }],
      ['a null entry in the errors list', { data: { flow: null }, errors: [null] }],
      ['a scalar entry in the errors list', { data: { flow: null }, errors: ['boom'] }],
    ])('rejects %s without throwing past fail()', async (_name, body) => {
      fetchMock.mockResolvedValue(jsonResponse(body))

      await expect(getFlow('f1')).rejects.toBeInstanceOf(ApproachApiError)
    })

    it('rejects an error with no path, which is a request-level failure', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          data: { repos: [] },
          errors: [{ message: 'internal error reading application state' }],
        }),
      )

      await expect(getRepos()).rejects.toMatchObject({ kind: 'graphql' })
    })

    it('rejects a response whose errors are only partly tolerated', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          data: { flow: { id: 'f1', epicProgression: null } },
          errors: [
            { message: 'internal error reading application state', path: ['flow', 'epicProgression'] },
            { message: 'internal error reading application state', path: ['flow', 'phases'] },
          ],
        }),
      )

      await expect(getFlow('f1')).rejects.toMatchObject({ kind: 'graphql' })
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

    // The deadline stays armed after the headers arrive, so a tunnel that sends
    // `200 OK` and then stalls aborts during the body read. Classifying that by
    // the status already in hand would call a hung tunnel a malformed 200.
    it.each([['TimeoutError'], ['AbortError']])(
      'maps a %s during the body read to a timeout, not the status',
      async (name) => {
        fetchMock.mockResolvedValue({
          ok: true,
          status: 200,
          json: () => Promise.reject(Object.assign(new Error('aborted'), { name })),
        } as unknown as Response)

        await expect(getRepos()).rejects.toMatchObject({ kind: 'timeout' })
      },
    )

    it('maps an unparseable body to an http error', async () => {
      fetchMock.mockResolvedValue(new Response('<html>gateway</html>', { status: 200 }))

      await expect(getRepos()).rejects.toMatchObject({ kind: 'http' })
    })

    // A *parseable* body that is not an object is the case the parse guard
    // misses: `null` and bare scalars are valid JSON, and reading `.errors` off
    // `null` throws a TypeError that `load()` rethrows straight past the
    // rendered panel into the generic boundary.
    it.each([
      ['null', null, 200, 'http'],
      ['a bare string', 'ok', 200, 'http'],
      ['a number', 0, 200, 'http'],
      // The status still classifies it, exactly as for an unparseable body.
      ['null behind a failing gateway', null, 504, 'timeout'],
    ])('maps %s to an ApproachApiError, not a TypeError', async (_label, body, status, kind) => {
      fetchMock.mockResolvedValue(jsonResponse(body, status as number))

      const error = await getRepos().catch((caught: unknown) => caught)

      expect(error).toBeInstanceOf(ApproachApiError)
      expect(error).toMatchObject({ kind })
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

  describe('payload shape', () => {
    // `{"data":{}}` is a valid envelope. Before these guards the selection came
    // back `undefined` and the page threw on `.length`, escaping the panel.
    it('rejects a data object missing the selection', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: {} }))

      const error = await getRepos().catch((caught: unknown) => caught)

      expect(error).toBeInstanceOf(ApproachApiError)
      expect(error).toMatchObject({ kind: 'http' })
    })

    it('rejects a scalar where a list was selected', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { repos: 'nope' } }))

      await expect(getRepos()).rejects.toBeInstanceOf(ApproachApiError)
    })

    it('rejects a scalar where a node was selected', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { flow: 7 } }))

      await expect(getFlow('f1')).rejects.toBeInstanceOf(ApproachApiError)
    })

    // `typeof [] === 'object'`, so an array would otherwise pass the node guard.
    it('rejects a list where a node was selected', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { repo: [] } }))

      await expect(getRepo('r1')).rejects.toBeInstanceOf(ApproachApiError)
    })

    // `null` is the API's real answer for an unknown id and must survive: the
    // routes turn it into a 404, which is not an error condition.
    it('passes a null node through for the 404 path', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { flow: null } }))

      await expect(getFlow('missing')).resolves.toBeNull()
    })

    it('passes an empty list through', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ data: { repos: [] } }))

      await expect(getRepos()).resolves.toEqual([])
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
