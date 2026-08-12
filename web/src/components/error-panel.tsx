export function ErrorPanel({ message }: { message: string }) {
  return (
    <div className="panel panel--error" role="alert">
      <p className="panel__title">Could not load Approach state</p>
      <p>{message}</p>
      <p className="panel__hint">
        The API is a local <code>approach serve</code> process reached over a tunnel, so this
        usually means it is stopped or the tunnel is down. Nothing is retried automatically —
        reload once it is back.
      </p>
    </div>
  )
}

export function EmptyPanel({ children }: { children: React.ReactNode }) {
  return <div className="panel">{children}</div>
}
