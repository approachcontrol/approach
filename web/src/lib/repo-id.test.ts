import { describe, expect, it } from 'vitest'

import { decodeRepoId, encodeRepoId } from './repo-id'

describe('repo id codec', () => {
  const paths = [
    '/Users/brian/dev/approach',
    '/Users/brian/dev/approachcontrol/approach-worktrees/flow-x',
    '/',
    '/tmp/repo with spaces/x',
    '/tmp/répo-ünicode/ok',
    '/tmp/a+b/c=d?e&f#g',
  ]

  it('round-trips path-shaped ids', () => {
    for (const path of paths) {
      expect(decodeRepoId(encodeRepoId(path))).toBe(path)
    }
  })

  it('never emits a character that needs URL escaping in a path segment', () => {
    for (const path of paths) {
      expect(encodeRepoId(path)).toMatch(/^[A-Za-z0-9_-]+$/)
    }
  })

  it('rejects segments outside the base64url alphabet', () => {
    expect(decodeRepoId('not/base64')).toBeNull()
    expect(decodeRepoId('has padding==')).toBeNull()
    expect(decodeRepoId('L1VzZXJz+w==')).toBeNull()
    expect(decodeRepoId('')).toBeNull()
  })

  it('rejects non-canonical encodings rather than guessing', () => {
    // Valid alphabet, but not what encodeRepoId would ever produce.
    expect(decodeRepoId('A')).toBeNull()
  })

  it('rejects an encoding of the empty string', () => {
    expect(decodeRepoId(encodeRepoId(''))).toBeNull()
  })
})
