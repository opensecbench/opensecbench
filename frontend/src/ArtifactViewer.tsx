import { useEffect, useState } from 'react'
import { api } from './api'
import { downloadArtifact } from './native'

// ArtifactViewer renders an artifact's HTML/text content in-app, inside a fully sandboxed iframe, so
// reports and transcripts never open in the external system browser — which couldn't carry the API
// token in a header (ADR-0061). "save" downloads the bytes via the native binding (still header-only).
export function ArtifactViewer({
  artifactId,
  title,
  downloadName,
  onClose,
}: {
  artifactId: string
  title: string
  downloadName: string
  onClose: () => void
}) {
  const [content, setContent] = useState<string | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    let cancelled = false
    setContent(null)
    setErr('')
    api
      .artifactContent(artifactId)
      .then((t) => {
        if (!cancelled) setContent(t)
      })
      .catch((e) => {
        if (!cancelled) setErr((e as Error).message)
      })
    return () => {
      cancelled = true
    }
  }, [artifactId])

  return (
    <div className="em-backdrop" onClick={onClose}>
      <div className="em-modal av-modal" onClick={(e) => e.stopPropagation()}>
        <div className="em-head">
          <span className="em-title">{title}</span>
          <button
            className="em-x"
            onClick={() => void downloadArtifact(api.artifactPath(artifactId), downloadName)}
          >
            save
          </button>
          <button className="em-x" onClick={onClose}>
            close ✕
          </button>
        </div>
        <div className="av-body">
          {err ? (
            <div className="error">{err}</div>
          ) : content === null ? (
            <div className="muted">Loading…</div>
          ) : (
            <iframe className="av-frame" sandbox="" srcDoc={content} title={title} />
          )}
        </div>
      </div>
    </div>
  )
}
