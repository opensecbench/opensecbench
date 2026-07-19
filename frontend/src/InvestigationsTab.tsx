import { useEffect, useState } from 'react'
import { api, Investigation, Project } from './api'

// Investigations (ADR-0028): follow-ups a disposition opened for observations needing validation (e.g. an
// unverified TruffleHog secret). "Investigate" starts a vuln-validator agent thread; findings it proposes
// are human-gated, so a person is always in the loop.
export function InvestigationsTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [items, setItems] = useState<Investigation[]>([])
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState<string | null>(null)

  async function load() {
    setItems((await api.listInvestigations(project.id)) ?? [])
  }

  useEffect(() => {
    if (!online) return
    void load().catch((e) => onError((e as Error).message))
    const t = setInterval(() => void load().catch(() => {}), 5000)
    return () => clearInterval(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  async function run(inv: Investigation) {
    setBusy(inv.id)
    setNote(null)
    try {
      await api.runInvestigation(inv.id)
      setNote('Investigation started — continue with the agent in the Analyst panel; any finding it proposes needs your approval.')
      await load()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  async function setStatus(inv: Investigation, status: string) {
    setBusy(inv.id)
    try {
      await api.setInvestigationStatus(inv.id, status)
      await load()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const open = items.filter((i) => i.status === 'open' || i.status === 'investigating')
  const closed = items.filter((i) => i.status === 'resolved' || i.status === 'dismissed')

  return (
    <div className="content">
      <div className="hero">
        <h1>Investigations</h1>
        <p>Follow-ups opened by post-run routing — validate, then confirm or dismiss. Findings stay human-gated.</p>
      </div>

      {note && <div className="banner">{note}</div>}

      <section className="panel">
        <div className="panel-head">Open {open.length > 0 && <span className="mc-pill amber">{open.length}</span>}</div>
        {open.length === 0 ? (
          <div className="empty">No open investigations.</div>
        ) : (
          <ul className="rows">
            {open.map((inv) => (
              <li key={inv.id} className="row-item">
                <span className={`badge ${inv.status}`}>{inv.status}</span>
                <span className="row-title">{inv.title}</span>
                <span className="grow" />
                {inv.status === 'open' && (
                  <button className="mini ok" disabled={!online || busy === inv.id} onClick={() => run(inv)}>
                    {busy === inv.id ? '…' : '🔎 Investigate'}
                  </button>
                )}
                <button className="mini" disabled={busy === inv.id} onClick={() => setStatus(inv, 'resolved')}>resolve</button>
                <button className="mini no" disabled={busy === inv.id} onClick={() => setStatus(inv, 'dismissed')}>dismiss</button>
              </li>
            ))}
          </ul>
        )}
      </section>

      {closed.length > 0 && (
        <section className="panel">
          <div className="panel-head">Closed</div>
          <ul className="rows">
            {closed.map((inv) => (
              <li key={inv.id} className="row-item">
                <span className={`badge ${inv.status}`}>{inv.status}</span>
                <span className="row-title muted">{inv.title}</span>
                <span className="grow" />
                {inv.status !== 'open' && (
                  <button className="mini" disabled={busy === inv.id} onClick={() => setStatus(inv, 'open')}>reopen</button>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}
