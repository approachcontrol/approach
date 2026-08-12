import { ApproachApiError } from './approach-api'

export type Loaded<T> = { ok: true; value: T } | { ok: false; message: string }

/**
 * Turns an expected API failure into a value the page can render.
 *
 * `error.tsx` still exists as the last-resort boundary, but in a production
 * build Next replaces a server-thrown error's message with a generic string
 * before it reaches that boundary — so the already-sanitized
 * `ApproachApiError` message is caught here instead, where it survives.
 * Anything else is genuinely unexpected and keeps propagating.
 */
export async function load<T>(fetcher: () => Promise<T>): Promise<Loaded<T>> {
  try {
    return { ok: true, value: await fetcher() }
  } catch (caught) {
    if (caught instanceof ApproachApiError) {
      return { ok: false, message: caught.message }
    }
    throw caught
  }
}
