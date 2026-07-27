import { useEffect, useRef, useState } from 'react'
import { api, CoverageView, Methodology, MethodologyCheck, MethodologySuggestion, Project } from './api'

const STATUSES = ['not_started', 'in_progress', 'covered', 'not_applicable']

// checkSummary collapses an item's checks into short chips: capability ids, and a count of agent/manual checks.
function checkChips(checks?: MethodologyCheck[]): { cls: string; label: string }[] {
  if (!checks || checks.length === 0) return [{ cls: 'manual', label: 'manual' }]
  return checks.map((c) =>
    c.kind === 'capability'
      ? { cls: 'cap', label: c.capability || 'capability' }
      : c.kind === 'agent'
        ? { cls: 'agent', label: `agent · ${c.profile || '?'}` }
        : { cls: 'manual', label: 'manual' },
  )
}

export function MethodologyTab({
  project,
  online,
  onError,
  onTestItem,
  reloadSignal,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
  onTestItem?: (itemId: string, itemTitle: string) => void
  reloadSignal?: number
}) {
  const [catalog, setCatalog] = useState<Methodology[]>([])
  const [view, setView] = useState<CoverageView | null>(null)
  const [suggestions, setSuggestions] = useState<MethodologySuggestion[]>([])
  const [adopt, setAdopt] = useState('')
  const [running, setRunning] = useState(false)
  const [runNote, setRunNote] = useState<string | null>(null)
  const pollRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  async function reload() {
    try {
      setView(await api.getMethodologyCoverage(project.id))
      setSuggestions((await api.methodologySuggestions(project.id)) ?? [])
    } catch (e) {
      onError((e as Error).message)
    }
  }

  // While any item has a task in flight, poll coverage so the panel tracks queued → running → tested
  // without a manual refresh (ADR-0056). Stops when nothing is active.
  useEffect(() => {
    const active = (view?.packs ?? []).some((p) => (p.items ?? []).some((ic) => ic.run_state))
    if (pollRef.current) {
      clearTimeout(pollRef.current)
      pollRef.current = null
    }
    if (active && online) {
      pollRef.current = setTimeout(() => void reload(), 2000)
    }
    return () => {
      if (pollRef.current) clearTimeout(pollRef.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, online])

  // Run the whole methodology, or one pack. Coverage then fills in as tasks complete (the poll picks it up).
  async function run(pack?: string) {
    setRunNote(null)
    setRunning(true)
    try {
      const res = await api.runMethodology(project.id, pack ? { pack } : undefined)
      const parts: string[] = []
      if (res.enqueued > 0) parts.push(`queued ${res.enqueued} scan${res.enqueued === 1 ? '' : 's'}`)
      if (res.agent_started > 0) parts.push(`started ${res.agent_started} agent check${res.agent_started === 1 ? '' : 's'}`)
      if (res.deferred_manual > 0) parts.push(`${res.deferred_manual} manual item${res.deferred_manual === 1 ? '' : 's'} need sign-off`)
      if (res.skipped?.length) parts.push(`${res.skipped.length} skipped`)
      setRunNote(parts.length ? 'Running · ' + parts.join(' · ') : 'Nothing to run')
      await reload()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setRunning(false)
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
  }, [online, project.id, reloadSignal])

  const adoptedIds = new Set((view?.packs ?? []).map((p) => p.id))
  const available = catalog.filter((m) => !adoptedIds.has(m.id))

  async function doAdoptID(id: string) {
    if (!id) return
    try {
      await api.adoptMethodology(project.id, id)
      setAdopt('')
      await reload()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  async function doUnadopt(id: string, title: string) {
    if (!window.confirm(`Remove the "${title}" methodology from this project? Coverage recorded against it is dropped.`)) return
    try {
      await api.unadoptMethodology(project.id, id)
      await reload()
    } catch (e) {
      onError((e as Error).message)
    }
  }
  async function doAdopt() {
    await doAdoptID(adopt)
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
  const packs = view?.packs ?? [] // Go serializes an empty slice as null

  return (
    <section className="panel">
      <div className="panel-head">
        Methodology & coverage
        <span className="grow" />
        {packs.length > 0 && (
          <button className="mrun-btn" disabled={!online || running} title="Run the adopted packs' capability checks against this project" onClick={() => void run()}>
            {running ? 'Starting…' : '▶ Run methodology'}
          </button>
        )}
      </div>

      {runNote && <div className="banner">{runNote}</div>}

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

      {suggestions.length > 0 && (
        <div className="banner">
          Suggested from the knowledge base:{' '}
          {suggestions.map((s) => (
            <button key={s.methodology_id} className="link" title={s.reason} onClick={() => { void doAdoptID(s.methodology_id) }}>
              adopt {s.title}
            </button>
          ))}
        </div>
      )}

      <div className="create-row">
        <select value={adopt} onChange={(e) => setAdopt(e.target.value)} disabled={!online || available.length === 0}>
          <option value="">{available.length ? 'Adopt a methodology…' : 'All packs adopted'}</option>
          {available.map((m) => (
            <option key={m.id} value={m.id}>{m.title} ({m.items?.length ?? 0})</option>
          ))}
        </select>
        <button onClick={doAdopt} disabled={!online || !adopt}>Adopt</button>
      </div>

      {packs.length === 0 && <div className="empty">No methodology adopted yet.</div>}

      {packs.map((p) => (
        <div key={p.id} className="mpack">
          <h3 className="mpack-head">
            {p.title} <span className="muted">{p.tech}</span>
            <span className="grow" />
            <button className="mpack-run" title="Run this pack's capability checks" disabled={!online || running} onClick={() => void run(p.id)}>▶ Run</button>
            <button className="mpack-remove" title="Remove this methodology from the project" disabled={!online} onClick={() => void doUnadopt(p.id, p.title)}>Remove</button>
          </h3>
          <ul className="mitems">
            {(p.items ?? []).map((ic) => (
              <li key={ic.item.id} className={`mitem status-${ic.status}`}>
                <div className="mitem-main">
                  <span className="mitem-title">{ic.item.title}</span>
                  {ic.item.objective && <span className="mitem-obj">{ic.item.objective}</span>}
                  <span className="mitem-checks">
                    {checkChips(ic.item.checks).map((chip, i) => (
                      <span key={i} className={`mchip ${chip.cls}`}>{chip.label}</span>
                    ))}
                  </span>
                  {ic.item.standards && ic.item.standards.length > 0 && (
                    <span className="mitem-std">{ic.item.standards.join(' · ')}</span>
                  )}
                </div>
                <div className="mitem-actions">
                  {ic.run_state && (
                    <span className={`mitem-runstate ${ic.run_state}`}>{ic.run_state === 'running' ? '● running' : '◦ queued'}</span>
                  )}
                  {(ic.evidence_count ?? 0) > 0 && (
                    <span className="mitem-ev" title="evidence attached to this item">🔬 {ic.evidence_count}</span>
                  )}
                  {onTestItem && (
                    <button className="ghost-btn" title="Open a Replay bound to this test item" onClick={() => onTestItem(ic.item.id, ic.item.title)}>
                      ↔ Test
                    </button>
                  )}
                  <select value={ic.status} onChange={(e) => setStatus(ic.item.id, e.target.value)} disabled={!online}>
                    {STATUSES.map((st) => (
                      <option key={st} value={st}>{st.replace('_', ' ')}</option>
                    ))}
                  </select>
                </div>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </section>
  )
}
