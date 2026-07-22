import { useEffect, useMemo, useState } from 'react'
import { api, Investigation, Observation, Project } from './api'
import { LocationChip, OpenCode } from './CodeLink'
import { DataTable, Column } from './DataTable'

const SEV_RANK: Record<string, number> = { critical: 4, high: 3, medium: 2, low: 1, info: 0 }
const INV_STATE: Record<string, string> = { open: 'open', investigating: 'running', resolved: 'resolved', dismissed: 'dismissed' }

// Investigations (ADR-0028): follow-ups opened for observations needing validation (e.g. an unverified
// TruffleHog secret, or one you sent from the Observations triage queue). "Investigate" starts a
// vuln-validator agent thread; findings it proposes are human-gated. Same spreadsheet interface as
// Observations/Findings: search/sort/filter the table, click a row to inspect and act in the side panel.
export function InvestigationsTab({
  project,
  online,
  observations,
  onOpenCode,
  onJump,
  onError,
}: {
  project: Project
  online: boolean
  observations: Observation[]
  onOpenCode: OpenCode
  onJump: (t: string) => void
  onError: (m: string) => void
}) {
  // The observation each investigation was opened for carries severity + source location (ADR-0050).
  const obsById = useMemo(() => new Map(observations.map((o) => [o.id, o])), [observations])
  const [items, setItems] = useState<Investigation[]>([])
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState('active')
  const [detailId, setDetailId] = useState<string | null>(null)

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

  async function setInvStatus(inv: Investigation, s: string) {
    setBusy(inv.id)
    try {
      await api.setInvestigationStatus(inv.id, s)
      await load()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const STATUS: { key: string; label: string; match: (i: Investigation) => boolean }[] = [
    { key: 'active', label: 'Active', match: (i) => i.status === 'open' || i.status === 'investigating' },
    { key: 'resolved', label: 'Resolved', match: (i) => i.status === 'resolved' },
    { key: 'dismissed', label: 'Dismissed', match: (i) => i.status === 'dismissed' },
    { key: 'all', label: 'All', match: () => true },
  ]
  const activeStatus = STATUS.find((s) => s.key === status) ?? STATUS[0]
  const q = search.trim().toLowerCase()
  const rows = useMemo(
    () =>
      items.filter((i) => {
        if (!activeStatus.match(i)) return false
        if (q && !`${i.title} ${obsById.get(i.observation_id)?.location ?? ''}`.toLowerCase().includes(q)) return false
        return true
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [items, status, q, obsById],
  )
  const detail = detailId ? items.find((i) => i.id === detailId) ?? null : null

  const columns: Column<Investigation>[] = [
    { key: 'severity', header: 'Sev', width: '72px', sortable: true, sortValue: (i) => SEV_RANK[obsById.get(i.observation_id)?.severity ?? ''] ?? -1, render: (i) => { const s = obsById.get(i.observation_id)?.severity; return s ? <span className={`sev sev-${s}`}>{s}</span> : <span className="muted">—</span> } },
    { key: 'title', header: 'Title', sortable: true, sortValue: (i) => i.title.toLowerCase(), render: (i) => <span className="dt-title">{i.title}</span> },
    { key: 'location', header: 'Location', className: 'mono', width: '200px', render: (i) => <span className="muted dt-ellip">{obsById.get(i.observation_id)?.location}</span> },
    { key: 'status', header: 'Status', width: '104px', sortable: true, sortValue: (i) => i.status, render: (i) => <span className={`badge ${i.status}`}>{INV_STATE[i.status] ?? i.status}</span> },
  ]

  return (
    <div className="table-page">
      <div className="hero compact">
        <h1>Investigations</h1>
        <p>
          The validation queue: uncertain signals flagged for a closer look. Run the agent to validate, then confirm —
          which promotes it to a <button className="link" onClick={() => onJump('findings')}>Finding</button> — or dismiss.
          Human-gated throughout.
        </p>
      </div>

      {note && <div className="banner">{note}</div>}

      <div className="table-toolbar">
        <input className="tt-search" placeholder="Search title, location…" value={search} onChange={(e) => setSearch(e.target.value)} />
        <div className="tt-chips">
          {STATUS.map((s) => (
            <button key={s.key} className={`chip ${status === s.key ? 'on' : ''}`} onClick={() => setStatus(s.key)}>
              {s.label} <span className="n">{items.filter(s.match).length}</span>
            </button>
          ))}
        </div>
        <span className="grow" />
        <span className="muted tt-count">{rows.length} shown</span>
      </div>

      <div className="table-split">
        <DataTable
          rows={rows}
          columns={columns}
          onRowClick={(i) => setDetailId(i.id)}
          activeId={detail?.id}
          defaultSort={{ key: 'severity', dir: 'desc' }}
          empty="No investigations. Send an observation here from the Observations tab to validate it."
        />
        {detail && (
          <aside className="detail-panel">
            <div className="dp-head">
              <span className={`badge ${detail.status}`}>{INV_STATE[detail.status] ?? detail.status}</span>
              <span className="dp-title">{detail.title}</span>
              <button className="dp-close" onClick={() => setDetailId(null)} aria-label="Close">✕</button>
            </div>
            <div className="dp-body">
              {(() => {
                const o = obsById.get(detail.observation_id)
                if (!o) return <p className="muted">The source observation is no longer available.</p>
                return (
                  <>
                    <div className="dp-meta">
                      <span className={`sev sev-${o.severity}`}>{o.severity}</span>
                      {o.rule_id && <span className="muted mono">{o.rule_id}</span>}
                    </div>
                    {o.location && <div className="dp-row"><span className="dp-k">Location</span><LocationChip obs={o} onOpenCode={onOpenCode} /></div>}
                    {o.detail && <p className="dp-detail">{o.detail}</p>}
                  </>
                )
              })()}
            </div>
            <div className="dp-actions">
              {detail.status === 'open' && (
                <button className="mini ok" disabled={!online || busy === detail.id} onClick={() => void run(detail)}>{busy === detail.id ? '…' : '🔎 Run agent'}</button>
              )}
              {(detail.status === 'open' || detail.status === 'investigating') ? (
                <>
                  <button className="mini" disabled={busy === detail.id} onClick={() => void setInvStatus(detail, 'resolved')}>Resolve</button>
                  <button className="mini no" disabled={busy === detail.id} onClick={() => void setInvStatus(detail, 'dismissed')}>Dismiss</button>
                </>
              ) : (
                <button className="mini" disabled={busy === detail.id} onClick={() => void setInvStatus(detail, 'open')}>↺ Reopen</button>
              )}
            </div>
          </aside>
        )}
      </div>
    </div>
  )
}
