/**
 * Timestamps render in UTC rather than a locale default: these pages are
 * server-rendered, so a locale format would silently follow the Vercel
 * region's clock instead of the reader's.
 */
export function formatTimestamp(value: string | null | undefined): string {
  if (!value) {
    return '—'
  }
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  return `${parsed.toISOString().slice(0, 16).replace('T', ' ')}Z`
}

export function formatCommit(value: string | null | undefined): string {
  if (!value) {
    return '—'
  }
  return value.length > 12 ? value.slice(0, 12) : value
}
