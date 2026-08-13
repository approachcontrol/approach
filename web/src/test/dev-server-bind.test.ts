import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

/**
 * The dev server's bind address is a security control, not a preference. This
 * app has no authentication of its own and fetches the API server-side with the
 * configured token, so a wildcard bind hands every Flow record on the machine
 * to anyone who can reach port 3000 — undoing the loopback-or-token posture
 * `approach serve` enforces on the Go side.
 *
 * Next's own default is `0.0.0.0`, which makes the safe value the one that has
 * to be written down, and a deleted flag the silent regression. Hence this test.
 */
const scripts: Record<string, string> = JSON.parse(
  readFileSync(fileURLToPath(new URL('../../package.json', import.meta.url)), 'utf8'),
).scripts

describe('package.json scripts', () => {
  it('binds the dev server to loopback', () => {
    expect(scripts.dev).toMatch(/(^|\s)(-H|--hostname)[\s=]127\.0\.0\.1(\s|$)/)
  })
})
