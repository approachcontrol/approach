import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

/**
 * `engines.node` is the only place that tells a developer which runtime to
 * install, and npm does not enforce it unless someone sets `engine-strict`. So
 * a range that drifts wider than the locked toolchain fails quietly: the range
 * said Node 23 was fine while the pinned Vitest declares
 * `^20.0.0 || ^22.0.0 || >=24.0.0`, which means the documented `npm test`
 * toolchain could not be installed on the runtime we pointed people at.
 *
 * The bound is derived from the lockfile rather than written down a second
 * time, because that drift arrives through a dependency bump, not through an
 * edit to this range. The invariant: every runtime the range advertises must be
 * one that every package we actually install accepts.
 */
type Version = [major: number, minor: number, patch: number]

type LockEntry = {
  optional?: boolean
  engines?: { node?: string }
}

function read(path: string): unknown {
  return JSON.parse(readFileSync(fileURLToPath(new URL(path, import.meta.url)), 'utf8'))
}

const declaredRange = (read('../../package.json') as { engines: { node: string } }).engines.node
const lockedPackages = (read('../../package-lock.json') as { packages: Record<string, LockEntry> })
  .packages

function compare(left: Version, right: Version): number {
  return left[0] - right[0] || left[1] - right[1] || left[2] - right[2]
}

function format(version: Version): string {
  return version.join('.')
}

/**
 * Only the comparator forms these lockfile ranges actually use are accepted —
 * `^`, `>=`, `>`, a bare version, and partial versions like `>= 0.4`. An
 * unrecognised form throws rather than silently matching nothing, so a range
 * grammar introduced by a future bump surfaces as a failure instead of a test
 * that quietly stops checking anything.
 */
function comparator(text: string): (version: Version) => boolean {
  const match = /^(\^|>=|>)?\s*(\d+)(?:\.(\d+))?(?:\.(\d+))?$/.exec(text.trim())
  if (!match) throw new Error(`unsupported semver comparator: ${text}`)

  const [, operator, major, minor, patch] = match
  const lower: Version = [Number(major), Number(minor ?? 0), Number(patch ?? 0)]

  if (operator === '>=') return (version) => compare(version, lower) >= 0
  if (operator === '>') return (version) => compare(version, lower) > 0
  if (operator === '^') {
    // `^0.y.z` is pinned to its minor; every other caret is pinned to its major.
    return (version) =>
      compare(version, lower) >= 0 &&
      version[0] === lower[0] &&
      (lower[0] !== 0 || version[1] === lower[1])
  }

  // A bare version constrains only the components it spells out: `18` is 18.x.
  const specified: number = minor === undefined ? 1 : patch === undefined ? 2 : 3
  return (version) => version.slice(0, specified).every((part, index) => part === lower[index])
}

function satisfies(version: Version, range: string): boolean {
  const trimmed = range.trim()
  if (trimmed === '' || trimmed === '*') return true
  return trimmed.split('||').some((part) => comparator(part)(version))
}

/**
 * Boundary versions around every release line the dependency tree mentions,
 * rather than a sample: the values that matter are the first and last version
 * of each line, which is exactly where an off-by-one range lands.
 */
const PROBES: Version[] = [
  [20, 0, 0],
  [20, 9, 0],
  [20, 18, 0],
  [20, 19, 0],
  [20, 19, 5],
  [21, 0, 0],
  [21, 7, 3],
  [22, 0, 0],
  [22, 11, 0],
  [22, 12, 0],
  [22, 13, 0],
  [22, 20, 0],
  [23, 0, 0],
  [23, 5, 0],
  [23, 11, 0],
  [24, 0, 0],
  [24, 10, 0],
  [25, 0, 0],
  [26, 0, 0],
]

describe('engines.node', () => {
  it('advertises only runtimes the locked dependency tree accepts', () => {
    const conflicts: string[] = []

    for (const probe of PROBES) {
      if (!satisfies(probe, declaredRange)) continue

      for (const [name, entry] of Object.entries(lockedPackages)) {
        // The root entry restates the range under test. Optional packages are
        // skipped by npm on an engine mismatch rather than failing the install,
        // which is why platform binaries pinned to one line do not constrain us.
        if (name === '' || entry.optional) continue

        const required = entry.engines?.node
        if (required && !satisfies(probe, required)) {
          conflicts.push(`Node ${format(probe)} is advertised but rejected by ${name} (${required})`)
        }
      }
    }

    expect(conflicts).toEqual([])
  })

  it('still admits a version on each supported release line', () => {
    for (const major of [20, 22, 24]) {
      const supported = PROBES.some(
        (probe) => probe[0] === major && satisfies(probe, declaredRange),
      )
      expect(supported, `Node ${major}.x should still have a supported version`).toBe(true)
    }
  })
})
