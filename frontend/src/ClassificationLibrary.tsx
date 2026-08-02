import { useEffect, useState } from 'react'
import { api, ClassificationLevel } from './api'

// ClassificationLibrary manages the data-classification scale (governance): the ordered set of levels that
// drives BOTH asset sensitivity and destination data clearance. Ships with three built-ins
// (open-source / internal / private) which can be renamed, recolored, and reordered but not deleted; the
// operator can add custom levels and delete unused custom ones. Least-sensitive sits at the top.
export function ClassificationLibrary({ online }: { online: boolean }) {
  const [levels, setLevels] = useState<ClassificationLevel[]>([])
  const [drafts, setDrafts] = useState<Record<string, string>>({}) // in-progress label edits by id
  const [newLabel, setNewLabel] = useState('')
  const [newColor, setNewColor] = useState('#8a90a6')
  const [error, setError] = useState<string | null>(null)

  async function load() {
    try {
      setLevels((await api.listClassificationLevels()) ?? [])
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    if (online) void load()
  }, [online])

  async function saveLevel(l: ClassificationLevel, patch: Partial<ClassificationLevel>) {
    try {
      await api.updateClassificationLevel(l.id, { label: patch.label ?? l.label, rank: patch.rank ?? l.rank, color: patch.color ?? l.color })
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  // Reorder by swapping this level's rank with its neighbour's (levels arrive sorted by rank ascending).
  async function move(i: number, dir: -1 | 1) {
    const a = levels[i]
    const b = levels[i + dir]
    if (!a || !b) return
    try {
      await api.updateClassificationLevel(a.id, { label: a.label, rank: b.rank, color: a.color })
      await api.updateClassificationLevel(b.id, { label: b.label, rank: a.rank, color: b.color })
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function add() {
    const label = newLabel.trim()
    if (!label) return
    const rank = (levels.length ? Math.max(...levels.map((l) => l.rank)) : 0) + 10 // new tiers land most-sensitive
    try {
      await api.createClassificationLevel({ label, rank, color: newColor })
      setNewLabel('')
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function remove(l: ClassificationLevel) {
    try {
      await api.deleteClassificationLevel(l.id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="lib-section">
      {error && <div className="banner error">⚠ {error}</div>}
      <div className="lib-head">
        <h2>Data classification</h2>
        <p>
          The ordered scale for both asset sensitivity and provider data clearance. A destination cleared for a level
          may receive that level and every less-sensitive one above it. Least-sensitive is at the top; built-in levels
          can be renamed, recolored, and reordered but not deleted.
        </p>
      </div>

      <div className="class-list">
        {levels.map((l, i) => (
          <div key={l.id} className="class-row">
            <span className="class-reorder">
              <button className="ghost-btn" title="More sensitive" disabled={!online || i === levels.length - 1} onClick={() => move(i, 1)}>↓</button>
              <button className="ghost-btn" title="Less sensitive" disabled={!online || i === 0} onClick={() => move(i, -1)}>↑</button>
            </span>
            <input
              type="color"
              className="class-color"
              value={l.color || '#8a90a6'}
              onChange={(e) => saveLevel(l, { color: e.target.value })}
              disabled={!online}
              title="Badge colour"
            />
            <input
              className="class-label"
              value={drafts[l.id] ?? l.label}
              onChange={(e) => setDrafts((d) => ({ ...d, [l.id]: e.target.value }))}
              onBlur={(e) => { if (e.target.value.trim() && e.target.value !== l.label) void saveLevel(l, { label: e.target.value.trim() }) }}
              disabled={!online}
            />
            <span className="class-id mono">{l.id}</span>
            {l.builtin ? (
              <span className="class-tag">built-in</span>
            ) : (
              <button className="del" title="Delete (only if unused)" onClick={() => remove(l)} disabled={!online}>✕</button>
            )}
          </div>
        ))}
      </div>

      <div className="class-add">
        <div className="prov-add-title">Add level</div>
        <input type="color" className="class-color" value={newColor} onChange={(e) => setNewColor(e.target.value)} disabled={!online} />
        <input placeholder="level name, e.g. Confidential" value={newLabel} onChange={(e) => setNewLabel(e.target.value)} disabled={!online} />
        <button className="prov-add-btn" onClick={add} disabled={!online || !newLabel.trim()}>＋ Add level</button>
        <div className="prov-hint">New levels are added as the most sensitive; reorder with the arrows. The id is derived from the name and is what assets/connections store.</div>
      </div>
    </div>
  )
}
