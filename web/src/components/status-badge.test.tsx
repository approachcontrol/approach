import { describe, expect, it } from 'vitest'

import { Badge, OutcomeBadge, StatusBadge } from './status-badge'

/**
 * These call the components as functions and inspect the returned element.
 * Statuses are free-form strings in the schema, so the mapping and its
 * fallback are logic worth pinning; none of it needs a DOM.
 *
 * This file is also the reason `vitest.config.mts` includes `.tsx`: under a
 * `.ts`-only glob it would be silently uncollected.
 */
function classOf(element: React.ReactElement): string {
  return (element.props as { className: string }).className
}

function textOf(element: React.ReactElement): unknown {
  return (element.props as { children: unknown }).children
}

describe('StatusBadge', () => {
  it('maps every status Approach is known to emit to a tone', () => {
    const expected: Record<string, string> = {
      pending: 'muted',
      in_progress: 'active',
      needs_attention: 'warn',
      blocked: 'danger',
      completed: 'good',
      merged: 'good',
      abandoned: 'muted',
      closed: 'muted',
      ready: 'active',
      running: 'active',
      skipped: 'muted',
    }

    for (const [status, tone] of Object.entries(expected)) {
      expect(classOf(StatusBadge({ status }))).toBe(`badge badge--${tone}`)
    }
  })

  it('falls back to a neutral tone for an unknown status rather than dropping it', () => {
    const element = StatusBadge({ status: 'invented_by_an_agent' })

    expect(classOf(element)).toBe('badge badge--muted')
    expect(textOf(element)).toBe('invented by an agent')
  })

  it('renders underscores as spaces and keeps an explicit label verbatim', () => {
    expect(textOf(StatusBadge({ status: 'needs_attention' }))).toBe('needs attention')
    expect(textOf(StatusBadge({ status: 'needs_attention', label: 'on: plan' }))).toBe('on: plan')
  })
})

describe('OutcomeBadge', () => {
  it('tones the four outcomes flowstore defines', () => {
    expect(classOf(OutcomeBadge({ outcome: 'approved' }))).toBe('badge badge--good')
    expect(classOf(OutcomeBadge({ outcome: 'approved_with_concerns' }))).toBe('badge badge--warn')
    expect(classOf(OutcomeBadge({ outcome: 'changes_requested' }))).toBe('badge badge--warn')
    expect(classOf(OutcomeBadge({ outcome: 'blocked' }))).toBe('badge badge--danger')
  })

  // Outcome is free text outside review phases, so a green badge would be a lie.
  it('does not render free-text outcomes as success', () => {
    const element = OutcomeBadge({ outcome: 'failed to reproduce' })

    expect(classOf(element)).toBe('badge badge--muted')
    expect(textOf(element)).toBe('failed to reproduce')
  })
})

describe('Badge', () => {
  it('defaults to the muted tone', () => {
    expect(classOf(Badge({ children: 'bare' }))).toBe('badge badge--muted')
  })

  it('honours an explicit tone', () => {
    expect(classOf(Badge({ children: 'auto', tone: 'active' }))).toBe('badge badge--active')
  })
})
