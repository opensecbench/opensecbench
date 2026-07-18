import { useEffect, useState } from 'react'
import { api, CoverageView, Methodology, Project } from './api'

const STATUSES = ['not_started', 'in_progress', 'covered', 'not_applicable']

export function MethodologyTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [catalog, setCatalog] = useState<Methodology[]>([])
  const [view, setView] = useState<CoverageView | null>(null)
  const [adopt, setAdopt] = useState('')

  async function reload() {
    try {
      setView(await api.getMethodologyCoverage(project.id))
    } catch (e) {
      onError((e as Error).message)
    }
  }

  useEffect(() => {
    if (!online) return
    void (async () => {
      try {
        setCatalog((await api.listMethodologies()) ?? [])
      } catch (e) {
        onError((e as Error).message)
      }
    })()
    void reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  const adoptedIds = new Set((view?.packs ?? []).map((p) => p.id))
  const available = catalog.filter((m) => !adoptedIds.has(m.id))

  async function doAdopt() {
    if (!adopt) return
    try {
      await api.adoptMethodology(project.id, adopt)
      setAdopt('')
      await reload()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  async function setStatus(itemId: string, status: string) {
    try {
      await api.setCoverage(project.id, itemId, status)
      await reload()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  const s = view?.summary

  return (
    <section className="panel">
      <div className="panel-head">Methodology & coverage</div>

      {s && s.total > 0 && (
        <div className="cov-summary">
          <div className="cov-bar">
            <div className="cov-fill" style={{ width: `${s.covered_pct}%` }} />
          </div>
          <div className="cov-nums">
            <b>{s.covered_pct}%</b> covered · {s.covered} covered · {s.in_progress} in progress ·{' '}
            {s.not_started} not started · {s.not_applicable} n/a
          </div>
        </div>
      )}

      <div className="create-row">
        <select value={adopt} onChange={(e) => setAdopt(e.target.value)} disabled={!online || available.length === 0}>
          <option value="">{available.length ? 'Adopt a methodology…' : 'All packs adopted'}</option>
          {available.map((m) => (
            <option key={m.id} value={m.id}>{m.title} ({m.items.length})</option>
          ))}
        </select>
        <button onClick={doAdopt} disabled={!online || !adopt}>Adopt</button>
      </div>

      {(!view || view.packs.length === 0) && <div className="empty">No methodology adopted yet.</div>}

      {view?.packs.map((p) => (
        <div key={p.id} className="mpack">
          <h3 className="mpack-head">{p.title} <span className="muted">{p.tech}</span></h3>
          <ul className="mitems">
            {p.items.map((ic) => (
              <li key={ic.item.id} className={`mitem status-${ic.status}`}>
                <div className="mitem-main">
                  <span className="mitem-title">{ic.item.title}</span>
                  {ic.item.objective && <span className="mitem-obj">{ic.item.objective}</span>}
                  {ic.item.standards && ic.item.standards.length > 0 && (
                    <span className="mitem-std">{ic.item.standards.join(' · ')}</span>
                  )}
                </div>
                <select value={ic.status} onChange={(e) => setStatus(ic.item.id, e.target.value)} disabled={!online}>
                  {STATUSES.map((st) => (
                    <option key={st} value={st}>{st.replace('_', ' ')}</option>
                  ))}
                </select>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </section>
  )
}
