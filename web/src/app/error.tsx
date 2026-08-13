'use client'

/**
 * Last-resort boundary. Expected API failures are caught in the pages (see
 * `lib/load.ts`) so their sanitized message survives a production build;
 * anything reaching here is unexpected, and Next has already replaced its
 * message with a generic string plus a server-side digest.
 */
export default function SegmentError({
  error,
  reset,
}: {
  error: Error & { digest?: string }
  reset: () => void
}) {
  return (
    <div className="panel panel--error" role="alert">
      <p className="panel__title">Something went wrong</p>
      <p>This page could not be rendered.</p>
      {error.digest ? <p className="panel__hint">Reference: {error.digest}</p> : null}
      <p className="panel__hint">
        <button type="button" onClick={reset}>
          Try again
        </button>
      </p>
    </div>
  )
}
