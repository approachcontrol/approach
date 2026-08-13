import { describe, expect, it } from 'vitest'

import { ApproachApiError } from './approach-api'
import { load } from './load'

describe('load', () => {
  it('wraps a successful fetch', async () => {
    await expect(load(async () => 'value')).resolves.toEqual({ ok: true, value: 'value' })
  })

  it('turns an ApproachApiError into a renderable failure carrying its kind', async () => {
    const error = new ApproachApiError('unauthorized', 'rejected')

    await expect(
      load(() => {
        throw error
      }),
    ).resolves.toEqual({ ok: false, message: 'rejected', kind: 'unauthorized' })
  })

  it('rethrows anything else, so a real bug still reaches the error boundary', async () => {
    const bug = new TypeError('cannot read properties of undefined')

    await expect(
      load(() => {
        throw bug
      }),
    ).rejects.toBe(bug)
  })

  it('does not swallow a rejected promise that is not an ApproachApiError', async () => {
    await expect(load(() => Promise.reject(new Error('boom')))).rejects.toThrow('boom')
  })
})
