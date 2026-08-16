import Link from 'next/link'
import { notFound } from 'next/navigation'

import { EmptyPanel, ErrorPanel } from '@/components/error-panel'
import { FlowTracking } from '@/components/flow-tracking'
import { Badge, OutcomeBadge, StatusBadge } from '@/components/status-badge'
import { getFlow, type FlowDetail, type Phase } from '@/lib/approach-api'
import { formatCommit, formatTimestamp } from '@/lib/format'
import { load } from '@/lib/load'
import { encodeRepoId } from '@/lib/repo-id'

export const dynamic = 'force-dynamic'
export const maxDuration = 30

export default async function FlowPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params

  const flow = await load(() => getFlow(id))
  if (!flow.ok) {
    return (
      <>
        <nav className="crumbs">
          <Link href="/">repositories</Link>
        </nav>
        <h1 className="page-title">Flow</h1>
        <ErrorPanel message={flow.message} kind={flow.kind} />
      </>
    )
  }
  if (flow.value === null) {
    notFound()
  }

  const detail = flow.value

  return (
    <>
      <nav className="crumbs">
        <Link href="/">repositories</Link>
        <span className="crumbs__sep">/</span>
        <Link href={`/repos/${encodeRepoId(detail.repo.id)}`}>{detail.repo.displayName}</Link>
      </nav>

      <h1 className="page-title">{detail.title}</h1>
      <div className="badge-row" style={{ marginBottom: '1.25rem' }}>
        <StatusBadge status={detail.status} />
        {detail.autoMode ? <Badge tone="active">auto mode</Badge> : <Badge>manual</Badge>}
      </div>

      <FlowFacts flow={detail} />
      <FlowTracking flow={detail} />

      <h2 className="section-heading">Phases ({detail.phases.length})</h2>
      {detail.phases.length === 0 ? (
        <EmptyPanel>This Flow has no phases.</EmptyPanel>
      ) : (
        <div className="card-list">
          {detail.phases.map((phase) => (
            <PhaseCard
              key={phase.id}
              phase={phase}
              isCurrent={phase.id === detail.currentPhase?.id}
            />
          ))}
        </div>
      )}
    </>
  )
}

function FlowFacts({ flow }: { flow: FlowDetail }) {
  return (
    <dl className="facts">
      <dt>Repository</dt>
      <dd>
        <Link href={`/repos/${encodeRepoId(flow.repo.id)}`}>{flow.repoPath}</Link>
      </dd>

      <dt>Worktree</dt>
      <dd>{flow.worktreePath ?? '—'}</dd>

      <dt>Branch</dt>
      <dd>{flow.branch ?? '—'}</dd>

      <dt>Base ref</dt>
      <dd>{flow.baseRef ?? '—'}</dd>

      <dt>Start commit</dt>
      <dd>{formatCommit(flow.commit)}</dd>

      <dt>Preset</dt>
      <dd>{flow.presetName ?? '—'}</dd>

      <dt>Plan</dt>
      <dd>{flow.planId ?? '—'}</dd>

      <dt>Created</dt>
      <dd>{formatTimestamp(flow.createdAt)}</dd>

      <dt>Updated</dt>
      <dd>{formatTimestamp(flow.updatedAt)}</dd>
    </dl>
  )
}

function PhaseCard({ phase, isCurrent }: { phase: Phase; isCurrent: boolean }) {
  return (
    <div className={isCurrent ? 'phase phase--current' : 'phase'}>
      <div className="phase__head">
        <span className="phase__title">
          <span className="phase__order">{phase.order}</span>
          {phase.title}
        </span>
        <span className="badge-row">
          {isCurrent ? <Badge tone="active">current</Badge> : null}
          <StatusBadge status={phase.status} />
        </span>
      </div>

      <div className="badge-row" style={{ marginTop: '0.35rem' }}>
        <Badge>{phase.kind}</Badge>
        {phase.outcome ? <OutcomeBadge outcome={phase.outcome} /> : null}
      </div>

      {phase.summary ? <p className="phase__summary">{phase.summary}</p> : null}

      <div className="phase__meta">
        id <code>{phase.id}</code>
        {phase.parentPhaseId ? <> · child of {phase.parentPhaseId}</> : null}
        {phase.dependsOn.length > 0 ? <> · after {phase.dependsOn.join(', ')}</> : null}
        {' · updated '}
        {formatTimestamp(phase.updatedAt)}
      </div>
    </div>
  )
}
