import { useEffect, useState } from 'react'
import { api } from './api'

// AssetEcosystems shows what the scanner auto-detected in a repo (marker files) and the operator's manual
// tags, and lets them add/remove tags. The scan gate unions detected + tags, so a tag is how you make a
// tool run on a stack detection missed (a polyglot monorepo, an unusual layout).
export function AssetEcosystems({ assetId, online, onError }: { assetId: string; online: boolean; onError: (m: string) => void }) {
  const [detected, setDetected] = useState<string[]>([])
  const [tags, setTags] = useState<string[]>([])
  const [input, setInput] = useState('')
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    api
      .getAssetEcosystems(assetId)
      .then((r) => {
        setDetected(r.detected ?? [])
        setTags(r.tags ?? [])
        setLoaded(true)
      })
      .catch((e) => onError((e as Error).message))
  }, [assetId, onError])

  async function save(next: string[]) {
    try {
      const a = await api.setAssetEcosystems(assetId, next)
      setTags(a.ecosystems ?? [])
    } catch (e) {
      onError((e as Error).message)
    }
  }
  function add() {
    const t = input.trim().toLowerCase()
    if (t && !tags.includes(t)) void save([...tags, t])
    setInput('')
  }

  if (!loaded) return null
  const detectedOnly = detected.filter((d) => !tags.includes(d))
  return (
    <div className="asset-eco">
      <span className="ae-label">stack</span>
      {detectedOnly.map((d) => (
        <span key={d} className="ae-tag detected" title="auto-detected from the repo">{d}</span>
      ))}
      {tags.map((t) => (
        <span key={t} className="ae-tag manual" title="manual tag">
          {t}
          <button className="ae-x" disabled={!online} onClick={() => void save(tags.filter((x) => x !== t))}>×</button>
        </span>
      ))}
      {detectedOnly.length === 0 && tags.length === 0 && <span className="ae-none">none detected</span>}
      <input
        className="ae-add"
        placeholder="+ tag"
        value={input}
        disabled={!online}
        onChange={(e) => setInput(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && add()}
      />
    </div>
  )
}
