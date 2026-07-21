import { useEffect, useState } from 'react'
import { api, Methodology } from './api'

// MethodologyCatalog is a read-only browse of the global methodology catalog (ADR-0009) — the built-in
// packs a project adopts from and tracks coverage against. There is no authoring today (the registry is
// code-defined); this surfaces what's available. Per-project adoption + coverage stay on the project's
// Methodology surface.
export function MethodologyCatalog({ online }: { online: boolean }) {
  const [packs, setPacks] = useState<Methodology[]>([])
  const [open, setOpen] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!online) return
    void api
      .listMethodologies()
      .then((m) => setPacks(m ?? []))
      .catch((e) => setError((e as Error).message))
  }, [online])

  return (
    <div className="lib-section">
      {error && <div className="banner error">⚠ {error}</div>}
      <div className="lib-head">
        <h2>Methodology catalog</h2>
        <p>The testing standards available across the instance. A project adopts a pack from its Methodology surface and tracks per-item coverage there — this is the shared catalog it draws from.</p>
      </div>

      {packs.length === 0 && <div className="orch-empty">No methodology packs registered.</div>}
      {packs.map((p) => {
        const expanded = open === p.id
        return (
          <div key={p.id} className="mcat-pack">
            <button className="mcat-pack-h" onClick={() => setOpen(expanded ? null : p.id)}>
              <span className="mcat-caret">{expanded ? '▾' : '▸'}</span>
              <b>{p.title}</b>
              <span className="mcat-meta">{p.tech} · v{p.version} · {p.items.length} item{p.items.length === 1 ? '' : 's'}</span>
            </button>
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
