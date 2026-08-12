/**
 * Statuses are free-form strings in the schema, not enums, so this maps the
 * values Approach is known to emit and falls back to a neutral badge for
 * anything else rather than dropping or mangling it.
 */

type Tone = 'active' | 'good' | 'warn' | 'danger' | 'muted'

const TONES: Record<string, Tone> = {
  // Flow statuses
  pending: 'muted',
  in_progress: 'active',
  needs_attention: 'warn',
  blocked: 'danger',
  completed: 'good',
  merged: 'good',
  abandoned: 'muted',
  closed: 'muted',
  // Phase statuses
  ready: 'active',
  running: 'active',
  skipped: 'muted',
}

export function StatusBadge({ status, label }: { status: string; label?: string }) {
  const tone = TONES[status] ?? 'muted'
  return <span className={`badge badge--${tone}`}>{label ?? status.replace(/_/g, ' ')}</span>
}

export function Badge({ children, tone = 'muted' }: { children: React.ReactNode; tone?: Tone }) {
  return <span className={`badge badge--${tone}`}>{children}</span>
}
