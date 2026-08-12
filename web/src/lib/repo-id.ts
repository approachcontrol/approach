/**
 * `Repo.id` is a normalized absolute filesystem path, so it carries slashes.
 * Percent-encoding a `/` as `%2F` inside a single dynamic route segment is
 * normalized and re-split inconsistently between Next's router, dev server,
 * and the edge proxy, so repo ids travel as base64url instead: the encoded
 * form uses only `[A-Za-z0-9_-]` and never needs escaping.
 */

const BASE64URL = /^[A-Za-z0-9_-]+$/

export function encodeRepoId(id: string): string {
  return Buffer.from(id, 'utf8').toString('base64url')
}

/**
 * Returns null for anything `encodeRepoId` would not have produced — a
 * hand-edited URL is a 404, never a decode exception or a silently mangled id.
 */
export function decodeRepoId(segment: string): string | null {
  if (!BASE64URL.test(segment)) {
    return null
  }
  const decoded = Buffer.from(segment, 'base64url').toString('utf8')
  if (decoded === '' || encodeRepoId(decoded) !== segment) {
    return null
  }
  return decoded
}
