import { useEffect, useState } from 'react'
import { api } from './api'

// ArtifactImage renders an artifact-backed image by fetching its bytes over an authenticated header
// and displaying them via an object URL, so the API token never rides in an <img src> URL (ADR-0061).
export function ArtifactImage({ id, alt, className }: { id: string; alt?: string; className?: string }) {
  const [src, setSrc] = useState('')

  useEffect(() => {
    let url = ''
    let cancelled = false
    api
      .artifactBlob(id)
      .then((b) => {
        if (cancelled) return
        url = URL.createObjectURL(b)
        setSrc(url)
      })
      .catch(() => {
        /* leave src empty on failure */
      })
    return () => {
      cancelled = true
      if (url) URL.revokeObjectURL(url)
    }
  }, [id])

  return <img className={className} src={src} alt={alt} />
}
