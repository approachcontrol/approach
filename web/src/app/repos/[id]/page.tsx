import Link from 'next/link'
import { notFound } from 'next/navigation'

import { EmptyPanel, ErrorPanel } from '@/components/error-panel'
import { Badge, StatusBadge } from '@/components/status-badge'
import { getRepo } from '@/lib/approach-api'
import { formatTimestamp } from '@/lib/format'
import { load } from '@/lib/load'
import { decodeRepoId } from '@/lib/repo-id'

export const dynamic = 'force-dynamic'
export const maxDuration = 30

export default async function RepoPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params
  // A hand-edited segment is a 404, never a decode exception.
  const repoId = decodeRepoId(id)
  if (repoId === null) {
    notFound()
  }

  const repo = await load(() => getRepo(repoId))
  if (!repo.ok) {
    return (
      <>
        <Crumbs />
        <h1 className="page-title">Repository</h1>
        <ErrorPanel message={repo.message} kind={repo.kind} />
      </>
    )
  }
  // Unknown ids resolve to null per the API contract.
  if (repo.value === null) {
    notFound()
  }

  const { displayName, path, isBare, inScanRoot, flows } = repo.value

  return (
    <>
      <Crumbs />
      <h1 className="page-title">{displayName}</h1>
      <p className="page-subtitle">{path}</p>

      <div className="badge-row">
        {inScanRoot ? <Badge tone="active">in scan root</Badge> : <Badge>outside scan root</Badge>}
        {isBare ? <Badge tone="warn">bare</Badge> : <Badge>worktree</Badge>}
      </div>

      <h2 className="section-heading">
        Flows ({flows.length}) — newest activity first
      </h2>

      {flows.length === 0 ? (
        <EmptyPanel>No Flows recorded for this repository.</EmptyPanel>
      ) : (
        <div className="card-list">
          {flows.map((flow) => (
            <Link
              key={flow.id}
              href={`/flows/${encodeURIComponent(flow.id)}`}
              className="card"
            >
              <div className="card__row">
                <span className="card__name">{flow.title}</span>
                <span className="card__aside">{formatTimestamp(flow.updatedAt)}</span>
              </div>
              <div className="badge-row" style={{ marginTop: '0.4rem' }}>
                <StatusBadge status={flow.status} />
                {flow.currentPhase ? (
                  <Badge tone="active">
                    on: {flow.currentPhase.title} ({flow.currentPhase.status.replace(/_/g, ' ')})
                  </Badge>
                ) : (
                  <Badge>no actionable phase</Badge>
                )}
                {flow.autoMode ? <Badge tone="active">auto</Badge> : null}
                {flow.branch ? <Badge>{flow.branch}</Badge> : null}
              </div>
            </Link>
          ))}
        </div>
      )}
    </>
  )
}

function Crumbs() {
  return (
    <nav className="crumbs">
      <Link href="/">repositories</Link>
    </nav>
  )
}
