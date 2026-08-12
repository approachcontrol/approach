import { describe, expect, it } from 'vitest'

import { formatCommit, formatTimestamp } from './format'

describe('formatTimestamp', () => {
  it('renders RFC3339 input in UTC regardless of the server offset', () => {
    expect(formatTimestamp('2026-08-12T03:53:15Z')).toBe('2026-08-12 03:53Z')
    expect(formatTimestamp('2026-08-11T23:53:15-04:00')).toBe('2026-08-12 03:53Z')
  })

  it('falls back to the raw value rather than "Invalid Date"', () => {
    expect(formatTimestamp('not a date')).toBe('not a date')
  })

  it('renders an em dash for an absent timestamp', () => {
    expect(formatTimestamp(null)).toBe('—')
    expect(formatTimestamp('')).toBe('—')
  })
})

describe('formatCommit', () => {
  it('abbreviates a full sha and leaves a short one alone', () => {
    expect(formatCommit('fc1624f6938a64575a429040bb84ba6e4c8c378c')).toBe('fc1624f6938a')
    expect(formatCommit('fc1624f')).toBe('fc1624f')
    expect(formatCommit(null)).toBe('—')
  })
})
