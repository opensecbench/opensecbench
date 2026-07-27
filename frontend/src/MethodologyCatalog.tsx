import { useCallback, useEffect, useState } from 'react'
import { api, Methodology } from './api'
import { MethodologyBuilder } from './MethodologyBuilder'

// MethodologyCatalog is the global authoring surface for methodology packs (ADR-0009 / ADR-0055) — the
// testing checklists a project adopts from and tracks coverage against. Built-in and extension packs are
// read-only; teams author their own here (or "Copy" a built-in into an editable pack), the same "grow your
// own library" pattern the PlaybookLibrary uses. Per-project adoption + coverage stay on the project's
// Methodology surface.
export function MethodologyCatalog({ online }: { online: boolean }) {
  const [packs, setPacks] = useState<Methodology[]>([])
  const [open, setOpen] = useState<string | null>(null)
  const [building, setBuilding] = useState(false)
  const [editing, setEditing] = useState<Methodology | null>(null)
  const [template, setTemplate] = useState<Methodology | null>(null) // "Copy" source (e.g. a built-in)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setPacks((await api.listMethodologies()) ?? [])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])
  useEffect(() => {
    if (online) void load()
  }, [online, load])

  // Open the builder pre-filled with a copy of `p` (create mode) — a built-in becomes a starting template.
  function copy(p: Methodology) {
    setEditing(null)
    setTemplate({ ...p, title: `${p.title} (copy)` })
    setBuilding(true)
  }

  async function del(id: string) {
    try {
      await api.deleteMethodology(id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="lib-section">
      {error && <div className="banner error">⚠ {error}</div>}
      <div className="lib-head">
        <h2>Methodology catalog</h2>
        <p>The testing standards available across the instance. Author your own packs here — or copy a built-in and edit it — then a project adopts a pack from its Methodology surface and tracks per-item coverage there.</p>
        <button className="lib-new" disabled={!online} onClick={() => { setEditing(null); setTemplate(null); setBuilding((v) => !v) }}>
          {building && !editing && !template ? 'Close' : '＋ New methodology'}
        </button>
      </div>

      {(building || editing) && (
        <MethodologyBuilder
          key={editing?.id ?? (template ? `copy:${template.id}` : 'new')}
          online={online}
          edit={editing ?? undefined}
          template={template ?? undefined}
          onCancel={() => { setBuilding(false); setEditing(null); setTemplate(null) }}
          onSaved={() => {
            setBuilding(false)
            setEditing(null)
            setTemplate(null)
            void load()
          }}
        />
      )}

      {packs.length === 0 && <div className="orch-empty">No methodology packs registered.</div>}
      {packs.map((p) => {
        const expanded = open === p.id
        return (
          <div key={p.id} className="mcat-pack">
            <div className="mcat-pack-h">
              <button className="mcat-pack-toggle" onClick={() => setOpen(expanded ? null : p.id)}>
                <span className="mcat-caret">{expanded ? '▾' : '▸'}</span>
                <b>{p.title}</b>
                <span className="mcat-meta">{p.tech} · v{p.version} · {p.items.length} item{p.items.length === 1 ? '' : 's'}</span>
              </button>
              <span className={p.builtin ? 'orch-builtin' : 'orch-saved'}>{p.builtin ? 'built-in' : 'saved'}</span>
              <button className="orch-copy" title="Save an editable copy of this pack" disabled={!online} onClick={() => copy(p)}>⧉ Copy</button>
              {!p.builtin && (
                <>
                  <button className="orch-edit" title="Edit this pack" disabled={!online} onClick={() => { setEditing(p); setTemplate(null); setBuilding(true) }}>✎ Edit</button>
                  <button className="orch-del" title="Delete this pack" disabled={!online} onClick={() => del(p.id)}>×</button>
                </>
              )}
            </div>
            {expanded && (
              <div className="mcat-items">
                {p.items.map((it) => (
                  <div key={it.id} className="mcat-item">
                    <div className="mcat-item-t">{it.title}</div>
                    {it.objective && <div className="mcat-item-d">{it.objective}</div>}
                    {(it.standards?.length ?? 0) > 0 && (
                      <div className="mcat-item-tags">{it.standards!.map((s) => <span key={s} className="mcat-tag">{s}</span>)}</div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
