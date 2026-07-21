import { useState } from 'react'
import { api, ReachabilityFact } from './api'

const VERDICT_COLOR: Record<string, string> = { reachable: '#dc2626', unreachable: '#6b7280', unknown: '#8792a5' }

// FindingReachability is an inline triage widget: it shows the aggregated reachability verdict for a
// finding (which sources agree, at what confidence) and lets an analyst add their own determination —
// the manual counterpart to the record_reachability agent tool. Adding a verdict re-evaluates disposition
// server-side, so a "reachable" call can escalate the finding.
export function FindingReachability({ projectId, subject, online, onError }: { projectId: string; subject: string; online: boolean; onError: (m: string) => void }) {
  const [open, setOpen] = useState(false)
  const [data, setData] = useState<{ reachable: string; confidence: string; facts: ReachabilityFact[] | null } | null>(null)
  const [rationale, setRationale] = useState('')
  const [busy, setBusy] = useState(false)

  async function load() {
    try {
      setData(await api.getReachability(projectId, 'observation', subject))
    } catch (e) {
      onError((e as Error).message)
    }
  }
  async function toggle() {
    const next = !open
    setOpen(next)
    if (next && !data) await load()
  }
  async function add(verdict: string) {
    setBusy(true)
    try {
      await api.addReachability(projectId, { subject_type: 'observation', subject, reachable: verdict, confidence: 'high', rationale: rationale.trim() })
      setRationale('')
      await load()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const verdict = data?.reachable ?? 'unknown'
  const facts = data?.facts ?? []
  return (
    <div className="reach">
      <button className="reach-toggle" onClick={toggle} title="Reachability">
        <span className="reach-dot" style={{ background: VERDICT_COLOR[open ? verdict : 'unknown'] }} />
        reachability{open && data ? `: ${verdict}${data.confidence ? ` (${data.confidence})` : ''}` : ''} {open ? '▾' : '▸'}
      </button>
      {open && (
        <div className="reach-body">
          {facts.length === 0 ? (
            <div className="reach-empty">No reachability facts yet — record your determination below.</div>
          ) : (
            <ul className="reach-facts">
              {facts.map((f) => (
                <li key={f.id} className="reach-fact">
                  <span className="rf-verdict" style={{ color: VERDICT_COLOR[f.reachable] }}>{f.reachable}</span>
                  <span className="rf-conf">{f.confidence}</span>
                  <span className="rf-source">{f.source}</span>
                  {f.rationale && <span className="rf-why muted">{f.rationale}</span>}
                </li>
              ))}
            </ul>
          )}
          <div className="reach-add">
            <input placeholder="why (call path, dispatch you traced)…" value={rationale} onChange={(e) => setRationale(e.target.value)} disabled={!online || busy} />
            <button className="reach-btn reachable" disabled={!online || busy} onClick={() => add('reachable')}>reachable</button>
            <button className="reach-btn" disabled={!online || busy} onClick={() => add('unreachable')}>unreachable</button>
          </div>
        </div>
      )}
    </div>
  )
}
