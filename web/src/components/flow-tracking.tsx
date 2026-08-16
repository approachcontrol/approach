import type { FlowDetail } from '@/lib/approach-api'
import { formatCommit, formatTimestamp } from '@/lib/format'

import { Badge, StatusBadge } from './status-badge'

/**
 * The Tracking section of the Flow detail page: the issue, pull request, and
 * merge an agent reported, plus the Bead the Flow is linked to and the durable
 * auto-progression state of that Bead's epic.
 *
 * It is a pure function of one `FlowDetail`, so every state below is assertable
 * from rendered output rather than from a running page.
 */
export function FlowTracking({ flow }: { flow: FlowDetail }) {
  const { issue, pullRequest, merge, bead } = flow
  // A Bead link is enough on its own: a Flow created from an epic child usually
  // has no issue, PR, or merge yet, and hiding the section would hide the only
  // linkage it has.
  if (!issue && !pullRequest && !merge && !bead) {
    return null
  }

  return (
    <>
      <h2 className="section-heading">Tracking</h2>
      <dl className="facts">
        {bead ? (
          <>
            <dt>Bead</dt>
            <dd>
              <code>{bead.id}</code>
              {bead.epicId ? <span> · epic <code>{bead.epicId}</code></span> : null}
            </dd>

            <dt>Progression</dt>
            <dd>
              <Progression flow={flow} />
            </dd>
          </>
        ) : null}

        {issue ? (
          <>
            <dt>Issue</dt>
            <dd>
              <Ref provider={issue.provider} number={issue.number} url={issue.url} fallback="issue" />
            </dd>
          </>
        ) : null}

        {pullRequest ? (
          <>
            <dt>Pull request</dt>
            <dd>
              <Ref
                provider={pullRequest.provider}
                number={pullRequest.number}
                url={pullRequest.url}
                fallback="pull request"
              />
              {pullRequest.headBranch || pullRequest.baseBranch ? (
                <span> · {pullRequest.headBranch ?? '?'} → {pullRequest.baseBranch ?? '?'}</span>
              ) : null}
              {pullRequest.status ? (
                <span> · <StatusBadge status={pullRequest.status} /></span>
              ) : null}
            </dd>
          </>
        ) : null}

        {merge ? (
          <>
            <dt>Merge</dt>
            <dd>
              <StatusBadge status={merge.status ?? 'pending'} />
              {merge.commit ? <span> · {formatCommit(merge.commit)}</span> : null}
              {merge.mergedAt ? <span> · {formatTimestamp(merge.mergedAt)}</span> : null}
            </dd>
          </>
        ) : null}
      </dl>
    </>
  )
}

/**
 * Four distinct states, read from the API rather than inferred:
 *
 * - `halted` whenever a halt reason is present, with the child, status, and
 *   message that stopped it;
 * - `done` only when the API says so — a manually disabled epic is not done,
 *   and deriving one from the other would report finished work that never ran;
 * - `enabled` / `disabled` otherwise.
 *
 * An epic link with no progression row is `not configured`, not `disabled`:
 * nobody has turned progression on for that epic, which is a different claim
 * from having turned it off.
 */
function Progression({ flow }: { flow: FlowDetail }) {
  const progression = flow.epicProgression
  if (!flow.bead?.epicId) {
    return <>—</>
  }
  if (!progression) {
    return <Badge>not configured</Badge>
  }
  if (progression.halt) {
    const { childBeadId, status, message } = progression.halt
    return (
      <>
        <Badge tone="danger">halted</Badge>
        <span> · <code>{childBeadId}</code> {status} · {message}</span>
      </>
    )
  }
  if (progression.done) {
    return <Badge tone="good">done</Badge>
  }
  return progression.enabled ? <Badge tone="active">enabled</Badge> : <Badge>disabled</Badge>
}

function Ref({
  provider,
  number,
  url,
  fallback,
}: {
  provider: string | null
  number: number | null
  url: string | null
  fallback: string
}) {
  const label = number !== null ? `${provider ?? fallback} #${number}` : (provider ?? fallback)
  if (!url) {
    return <>{label}</>
  }
  return (
    <a href={url} rel="noreferrer noopener" target="_blank">
      {label}
    </a>
  )
}
