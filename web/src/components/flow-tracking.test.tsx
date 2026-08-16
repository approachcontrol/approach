import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { EpicProgression, FlowDetail } from '@/lib/approach-api'

import { FlowTracking } from './flow-tracking'

/**
 * These assert rendered markup rather than element props: the point of the
 * section is what a reader can see, and the progression states are only
 * distinguishable in the output.
 */
function render(flow: Partial<FlowDetail>): string {
  return renderToStaticMarkup(<FlowTracking flow={flowDetail(flow)} />)
}

function flowDetail(overrides: Partial<FlowDetail>): FlowDetail {
  return {
    id: 'flow-1',
    title: 'A Flow',
    status: 'in_progress',
    repoPath: '/repos/alpha',
    repo: { id: '/repos/alpha', displayName: 'alpha' },
    worktreePath: null,
    branch: null,
    baseRef: null,
    commit: null,
    presetName: null,
    planId: null,
    autoMode: false,
    bead: null,
    epicProgression: null,
    epicProgressionUnavailable: false,
    issue: null,
    pullRequest: null,
    merge: null,
    phases: [],
    currentPhase: null,
    createdAt: '2026-08-11T12:00:00Z',
    updatedAt: '2026-08-11T12:00:00Z',
    ...overrides,
  }
}

function progression(overrides: Partial<EpicProgression>): EpicProgression {
  return { enabled: false, done: false, halt: null, ...overrides }
}

describe('FlowTracking', () => {
  it('renders nothing for a Flow with no linkage at all', () => {
    expect(render({})).toBe('')
  })

  it('renders for a child-only Bead link, with no epic and no progression row at all', () => {
    const markup = render({ bead: { id: 'approach-y7g.7', epicId: null } })

    expect(markup).toContain('Tracking')
    expect(markup).toContain('approach-y7g.7')
    expect(markup).not.toContain('epic')
    // No epic means there is nothing to report, so the row is omitted rather
    // than filled in — and certainly not reported as "disabled".
    expect(markup).not.toContain('Progression')
    expect(markup).not.toContain('not configured')
    expect(markup).not.toContain('disabled')
  })

  // The API trims before looking a row up, so it would never find one for this
  // link. Rendering an empty epic and a progression verdict would claim more
  // than the server was ever asked.
  it('treats a whitespace-only epic id as no epic, the way the API does', () => {
    const markup = render({ bead: { id: 'approach-y7g.7', epicId: '   ' } })

    expect(markup).toContain('approach-y7g.7')
    expect(markup).not.toContain('epic')
    expect(markup).not.toContain('Progression')
  })

  it('keeps the child and epic ids when the epic has no progression row', () => {
    const markup = render({
      bead: { id: 'approach-y7g.7', epicId: 'approach-y7g' },
      epicProgression: null,
    })

    expect(markup).toContain('<code>approach-y7g.7</code>')
    expect(markup).toContain('<code>approach-y7g</code>')
    // A missing row is "nobody turned this on", which is not the same as off.
    expect(markup).toContain('not configured')
    expect(markup).not.toContain('disabled')
  })

  // The API nulls epicProgression for an unreadable row and reports the failure
  // out of band, so this state arrives as the same null a missing row does.
  // Rendering it as "not configured" would claim nobody enabled progression for
  // an epic nothing managed to look at.
  it('separates a row that could not be read from an epic with no row', () => {
    const bead = { id: 'approach-y7g.7', epicId: 'approach-y7g' }
    const markup = render({ bead, epicProgression: null, epicProgressionUnavailable: true })

    expect(markup).toContain('<code>approach-y7g</code>')
    expect(markup).toContain('unavailable')
    expect(markup).not.toContain('not configured')
    expect(markup).not.toContain('disabled')
    expect(markup).not.toContain('enabled')
  })

  it('distinguishes enabled, disabled, done, and halted', () => {
    const bead = { id: 'approach-y7g.7', epicId: 'approach-y7g' }
    const labels = {
      enabled: render({ bead, epicProgression: progression({ enabled: true }) }),
      disabled: render({ bead, epicProgression: progression({}) }),
      done: render({ bead, epicProgression: progression({ done: true }) }),
      halted: render({
        bead,
        epicProgression: progression({
          halt: {
            childBeadId: 'approach-y7g.4',
            status: 'needs_attention',
            message: 'child Flow needs attention',
          },
        }),
      }),
    }

    expect(labels.enabled).toContain('enabled')
    expect(labels.disabled).toContain('disabled')
    expect(labels.done).toContain('done')
    expect(labels.halted).toContain('halted')
    // Distinct, not merely present: "done" must not read as "enabled", and a
    // halted epic must not be reported as simply disabled.
    expect(labels.done).not.toContain('enabled')
    expect(labels.halted).not.toContain('>disabled<')
    expect(labels.disabled).not.toContain('>done<')
    expect(new Set(Object.values(labels)).size).toBe(4)
  })

  it('names the child, status, and message that halted progression', () => {
    const markup = render({
      bead: { id: 'approach-y7g.7', epicId: 'approach-y7g' },
      epicProgression: progression({
        halt: {
          childBeadId: 'approach-y7g.4',
          status: 'blocked',
          message: 'the child Flow is blocked on review',
        },
      }),
    })

    expect(markup).toContain('approach-y7g.4')
    expect(markup).toContain('blocked')
    expect(markup).toContain('the child Flow is blocked on review')
  })

  it('renders the halt status as a badge, not as a raw underscored value', () => {
    const markup = render({
      bead: { id: 'approach-y7g.7', epicId: 'approach-y7g' },
      epicProgression: progression({
        halt: {
          childBeadId: 'approach-y7g.4',
          status: 'needs_attention',
          message: 'child Flow needs attention',
        },
      }),
    })

    // The same treatment every other status on the page gets: a toned badge
    // with the underscore spelled out.
    expect(markup).toContain('<span class="badge badge--warn">needs attention</span>')
    expect(markup).not.toContain('>needs_attention<')
  })

  it('reports only the explicit done flag, never an inference from enabled', () => {
    // Disabled with no halt is an epic somebody turned off; calling it done
    // would claim the epic's children all finished.
    expect(render({
      bead: { id: 'approach-y7g.7', epicId: 'approach-y7g' },
      epicProgression: progression({}),
    })).not.toContain('done')
  })

  it('keeps issue, pull request, and merge rows alongside Bead data', () => {
    const markup = render({
      bead: { id: 'approach-y7g.7', epicId: 'approach-y7g' },
      epicProgression: progression({ enabled: true }),
      issue: { provider: 'github', number: 12, url: 'https://example.test/issues/12' },
      pullRequest: {
        provider: 'github',
        number: 34,
        url: 'https://example.test/pull/34',
        headBranch: 'flow/api',
        baseBranch: 'main',
        status: 'open',
      },
      merge: { status: 'merged', commit: 'deadbeefcafe', mergedAt: '2026-08-11T14:00:00Z' },
    })

    expect(markup).toContain('github #12')
    expect(markup).toContain('github #34')
    expect(markup).toContain('flow/api')
    expect(markup).toContain('merged')
    expect(markup).toContain('approach-y7g.7')
    expect(markup).toContain('enabled')
  })

  it('still renders for issue, pull request, or merge data with no Bead link', () => {
    const markup = render({
      issue: { provider: 'github', number: 12, url: null },
    })

    expect(markup).toContain('github #12')
    expect(markup).not.toContain('Bead')
  })
})
