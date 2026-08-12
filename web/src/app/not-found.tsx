import Link from 'next/link'

export default function NotFound() {
  return (
    <>
      <h1 className="page-title">Not found</h1>
      <div className="panel">
        <p style={{ marginTop: 0 }}>
          No repository or Flow matches that URL. IDs resolve against a live snapshot, so a Flow
          that was deleted — or a repository the server can no longer see — reads the same as one
          that never existed.
        </p>
        <p style={{ marginBottom: 0 }}>
          <Link href="/">Back to repositories</Link>
        </p>
      </div>
    </>
  )
}
