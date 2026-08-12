import Link from 'next/link'

import { EmptyPanel, ErrorPanel } from '@/components/error-panel'
import { Badge } from '@/components/status-badge'
import { getRepos } from '@/lib/approach-api'
import { load } from '@/lib/load'
import { encodeRepoId } from '@/lib/repo-id'

// Each request rebuilds a live snapshot on the API side; nothing here is
// prerenderable. `maxDuration` must exceed the client's 25s abort so a slow
// scan surfaces as this app's error state rather than a platform timeout.
export const dynamic = 'force-dynamic'
export const maxDuration = 30

export default async function ReposPage() {
  const repos = await load(getRepos)

  return (
    <>
      <h1 className="page-title">Repositories</h1>
      <p className="page-subtitle">
        Repositories discovered under the scan root, unioned with every repository a Flow
        references.
      </p>

      {!repos.ok ? (
        <ErrorPanel message={repos.message} />
      ) : repos.value.length === 0 ? (
        <EmptyPanel>
          No repositories. The server found nothing under its scan root and no Flow references a
          repository.
        </EmptyPanel>
      ) : (
        <div className="card-list">
          {repos.value.map((repo) => (
            <Link key={repo.id} href={`/repos/${encodeRepoId(repo.id)}`} className="card">
              <div className="card__row">
                <span className="card__name">{repo.displayName}</span>
                <span className="card__aside">
                  {repo.flows.length} {repo.flows.length === 1 ? 'flow' : 'flows'}
                </span>
              </div>
              <div className="card__meta">{repo.path}</div>
              <div className="badge-row" style={{ marginTop: '0.4rem' }}>
                {repo.inScanRoot ? (
                  <Badge tone="active">in scan root</Badge>
                ) : (
                  <Badge>outside scan root</Badge>
                )}
                {repo.isBare ? <Badge tone="warn">bare</Badge> : null}
              </div>
            </Link>
          ))}
        </div>
      )}
    </>
  )
}
