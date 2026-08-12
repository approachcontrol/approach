import { describe, expect, it } from 'vitest'

import nextConfig from '../../next.config'

/**
 * The header rules have regressed once already: a blanket `no-store` on
 * `/:path*` also hit content-hashed `/_next/static` assets and re-downloaded
 * every chunk on every page load. The fix is a negative lookahead in a route
 * source string — exactly the kind of thing that breaks invisibly — so it gets
 * pinned here rather than only being checked by hand against a running server.
 *
 * Next compiles this source to `^(?:/((?!_next/static/).*))(?:/)?$`, and the
 * source is already valid regex syntax, so anchoring it directly is a faithful
 * proxy for the routing behavior.
 */
async function headerRules() {
  const rules = await nextConfig.headers!()
  return rules.map((rule) => ({
    source: rule.source,
    keys: rule.headers.map((header) => header.key),
    value: (key: string) => rule.headers.find((header) => header.key === key)?.value,
  }))
}

function matcher(source: string): RegExp {
  return new RegExp(`^${source}$`)
}

describe('next.config headers', () => {
  it('applies the security headers to every path, assets included', async () => {
    const rule = (await headerRules()).find((candidate) => candidate.source === '/:path*')

    expect(rule).toBeDefined()
    expect(rule!.keys).toEqual(['X-Content-Type-Options', 'Referrer-Policy'])
    expect(rule!.value('X-Content-Type-Options')).toBe('nosniff')
    expect(rule!.value('Referrer-Policy')).toBe('no-referrer')
  })

  it('declares exactly one no-store rule, and it excludes hashed assets', async () => {
    const noStore = (await headerRules()).filter(
      (rule) => rule.value('Cache-Control') === 'no-store',
    )

    expect(noStore).toHaveLength(1)
    expect(noStore[0].source).toBe('/((?!_next/static/).*)')
  })

  it('matches every route that renders Flow state', async () => {
    const [{ source }] = (await headerRules()).filter(
      (rule) => rule.value('Cache-Control') === 'no-store',
    )
    const matches = matcher(source)

    for (const path of [
      '/',
      '/repos/L1VzZXJzL2JyaWFu',
      '/flows/approach-j2i',
      '/nope',
      // A path segment that merely contains the asset prefix is still a page.
      '/repos/_next/static',
      '/foo/_next/static/x.js',
    ]) {
      expect(matches.test(path), `${path} should be no-store`).toBe(true)
    }
  })

  it('does not match content-hashed assets, which must stay cacheable', async () => {
    const [{ source }] = (await headerRules()).filter(
      (rule) => rule.value('Cache-Control') === 'no-store',
    )
    const matches = matcher(source)

    for (const path of [
      '/_next/static/chunks/main.js',
      '/_next/static/css/app.css',
      '/_next/static/media/font.woff2',
    ]) {
      expect(matches.test(path), `${path} should keep its immutable caching`).toBe(false)
    }
  })
})
