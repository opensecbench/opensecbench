import { useEffect, useRef, useState } from 'react'
import { api, CoverageView, Methodology, MethodologyCheck, MethodologySuggestion, Project } from './api'

const STATUSES = ['not_started', 'in_progress', 'covered', 'not_applicable']
// Human labels for the checklist reframe — the stored states stay the same (not_started/in_progress/
// covered/not_applicable); only what the user reads changes.
const STATUS_LABEL: Record<string, string> = { not_started: 'To do', in_progress: 'In progress', covered: 'Done', not_applicable: 'N/A' }

// isManualItem is true when nothing runs automatically for the item — no capability or agent check — so its
// only path to covered is a human sign-off (ADR-0056 P3).
function isManualItem(checks?: MethodologyCheck[]): boolean {
  return !(checks ?? []).some((c) => c.kind === 'capability' || c.kind === 'agent')
}

// checkChips collapses an item's checks into short chips: capability ids, agent profiles, or a manual marker.
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
    if (!window.confirm(`Remove the "${title}" checklist from this project? Your progress on it is dropped.`)) return
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

  // Manual items have no automation — a human signs them off with an optional note (ADR-0056 P3).
  async function signOff(itemId: string) {
    const note = window.prompt('Sign-off note (optional) — what you verified:')
    if (note === null) return // canceled
    try {
      await api.setCoverage(project.id, itemId, 'covered', note)
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
        Checklist
        <span className="grow" />
        {packs.length > 0 && (
          <button className="mrun-btn" disabled={!online || running} title="Run the checks that can auto-tick items (scanners, code-analysis agents)" onClick={() => void run()}>
            {running ? 'Starting…' : '▶ Run auto-checks'}
          </button>
        )}
      </div>

      {runNote && <div className="banner">{runNote}</div>}

      {s && s.total > 0 && (
        <div className="cl-summary">
          <div className="cl-counts">
            <span className="c-done"><b>{s.covered}</b> done</span>
            <span className="c-prog"><b>{s.in_progress}</b> in progress</span>
            <span className="c-todo"><b>{s.not_started}</b> to do</span>
            <span className="c-na"><b>{s.not_applicable}</b> N/A</span>
          </div>
          <div className="cl-bar">
            <i className="b-done" style={{ width: `${(s.covered / s.total) * 100}%` }} />
            <i className="b-prog" style={{ width: `${(s.in_progress / s.total) * 100}%` }} />
          </div>
          <div className="cl-barlbl">{s.covered} of {s.total} worked through · not a security score</div>
        </div>
      )}

      {suggestions.length > 0 && (
        <div className="banner">
          Suggested from the knowledge base:{' '}
          {suggestions.map((s) => (
            <button key={s.methodology_id} className="link" title={s.reason} onClick={() => { void doAdoptID(s.methodology_id) }}>
              add {s.title}
            </button>
          ))}
        </div>
      )}

      <div className="cl-add">
        <div className="cl-add-h">{packs.length ? 'Add another checklist' : 'Pick a checklist to work through'}</div>
        <div className="cl-add-row">
          <select value={adopt} onChange={(e) => setAdopt(e.target.value)} disabled={!online || available.length === 0}>
            <option value="">{available.length ? 'Choose a checklist…' : catalog.length ? 'All checklists added' : 'No checklist templates yet'}</option>
            {available.map((m) => (
              <option key={m.id} value={m.id}>{m.title} — {m.items?.length ?? 0} item{(m.items?.length ?? 0) === 1 ? '' : 's'}</option>
            ))}
          </select>
          <button className="cl-add-btn" onClick={doAdopt} disabled={!online || !adopt}>＋ Add</button>
        </div>
        {catalog.length === 0 && (
          <div className="hint">No checklist templates exist yet. Create one in the Library under “Checklists” — build it by hand, import JSON, or paste a free-form list — then add it here.</div>
        )}
      </div>

      {packs.map((p) => (
        <div key={p.id} className="mpack">
          <h3 className="mpack-head">
            {p.title} <span className="muted">{p.tech}</span>
            <span className="grow" />
            <button className="mpack-run" title="Run this checklist's auto-checks" disabled={!online || running} onClick={() => void run(p.id)}>▶ Run</button>
            <button className="mpack-remove" title="Remove this checklist from the project" disabled={!online} onClick={() => void doUnadopt(p.id, p.title)}>Remove</button>
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
                  {ic.note && <span className="mitem-note">{ic.note}</span>}
                </div>
                <div className="mitem-actions">
                  {ic.run_state && (
                    <span className={`mitem-runstate ${ic.run_state}`}>{ic.run_state === 'running' ? '● running' : '◦ queued'}</span>
                  )}
                  {(ic.finding_count ?? 0) > 0 && (
                    <span className={`mitem-find sev-${ic.finding_severity || 'info'}`} title="findings linked to this item (separate from checklist status)">
                      ▲ {ic.finding_count}{ic.finding_severity ? ` ${ic.finding_severity}` : ''}
                    </span>
                  )}
                  {(ic.evidence_count ?? 0) > 0 && (
                    <span className="mitem-ev" title="evidence attached to this item">🔬 {ic.evidence_count}</span>
                  )}
                  {isManualItem(ic.item.checks) && ic.status !== 'covered' && (
                    <button className="ghost-btn" title="Manually sign off this item" onClick={() => void signOff(ic.item.id)}>✓ Sign off</button>
                  )}
                  {onTestItem && (
                    <button className="ghost-btn" title="Open a Replay bound to this test item" onClick={() => onTestItem(ic.item.id, ic.item.title)}>
                      ↔ Test
                    </button>
                  )}
                  <select value={ic.status} onChange={(e) => setStatus(ic.item.id, e.target.value)} disabled={!online}>
                    {STATUSES.map((st) => (
                      <option key={st} value={st}>{STATUS_LABEL[st] ?? st}</option>
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
