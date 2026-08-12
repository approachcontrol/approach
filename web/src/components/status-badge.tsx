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

/**
 * Phase outcomes are a closed set only for review phases; every other kind is
 * free text, so an agent-written "failed to reproduce" must not render green.
 * Anything unrecognized stays neutral. The four known values are the
 * `Outcome*` constants in `flowstore/store.go`.
 */
const OUTCOME_TONES: Record<string, Tone> = {
  approved: 'good',
  approved_with_concerns: 'warn',
  changes_requested: 'warn',
  blocked: 'danger',
}

export function OutcomeBadge({ outcome }: { outcome: string }) {
  const tone = OUTCOME_TONES[outcome] ?? 'muted'
  return <span className={`badge badge--${tone}`}>{outcome.replace(/_/g, ' ')}</span>
}

export function Badge({ children, tone = 'muted' }: { children: React.ReactNode; tone?: Tone }) {
  return <span className={`badge badge--${tone}`}>{children}</span>
}
