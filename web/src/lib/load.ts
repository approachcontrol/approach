import { ApproachApiError, type ApproachApiErrorKind } from './approach-api'

export type Loaded<T> =
  | { ok: true; value: T }
  | { ok: false; message: string; kind: ApproachApiErrorKind }

/**
 * Turns an expected API failure into a value the page can render.
 *
 * `error.tsx` still exists as the last-resort boundary, but in a production
 * build Next replaces a server-thrown error's message with a generic string
 * before it reaches that boundary — so the already-sanitized
 * `ApproachApiError` message is caught here instead, where it survives.
 * Anything else is genuinely unexpected and keeps propagating.
 *
 * The kind travels with the message so the panel can name the fix; the message
 * alone cannot distinguish a stale token from a stopped tunnel.
 */
export async function load<T>(fetcher: () => Promise<T>): Promise<Loaded<T>> {
  try {
    return { ok: true, value: await fetcher() }
  } catch (caught) {
    if (caught instanceof ApproachApiError) {
      return { ok: false, message: caught.message, kind: caught.kind }
    }
    throw caught
  }
}
