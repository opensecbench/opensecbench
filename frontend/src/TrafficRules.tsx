import { useEffect, useMemo, useState } from 'react'
import { api, RuleAction, TrafficRule } from './api'

// TrafficRules is the unified proxy rules editor (ADR-0016): an ordered list of CEL match → action rules.
// Evaluated top→bottom per phase — modify actions fall through, hold/drop are terminal. Replaces the old
// regex match/replace and the intercept on/off toggles (interception is now the "hold" action).
const ACTIONS: { value: RuleAction; label: string; cls: string }[] = [
  { value: 'hold', label: 'Hold — pause for manual edit', cls: 'hold' },
  { value: 'drop', label: 'Drop — block it', cls: 'drop' },
  { value: 'set_header', label: 'Set header', cls: 'hdr' },
  { value: 'remove_header', label: 'Remove header', cls: 'hdr' },
  { value: 'replace_body', label: 'Replace in body', cls: 'body' },
  { value: 'set_status', label: 'Set response status', cls: 'status' },
]
const actMeta = (a: RuleAction) => ACTIONS.find((x) => x.value === a)!

// A short label for the collapsed row's action chip.
function actionSummary(r: TrafficRule): string {
  switch (r.action) {
    case 'set_header': return `SET ${r.params.header_name || 'header'}`
    case 'remove_header': return `RM ${r.params.header_name || 'header'}`
    case 'replace_body': return 'REPLACE body'
    case 'set_status': return `STATUS ${r.params.status ?? ''}`
    default: return r.action.toUpperCase()
  }
}

const blankRule = (): TrafficRule => ({ enabled: true, phase: 'request', match: '', action: 'hold', params: {} })

export function TrafficRules({ project, online, onError }: { project: { id: string }; online: boolean; onError: (m: string) => void }) {
  const [rules, setRules] = useState<TrafficRule[]>([])
  const [saved, setSaved] = useState<TrafficRule[]>([])
  const [openIdx, setOpenIdx] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)

  async function load() {
    try {
      const rs = (await api.listTrafficRules(project.id)) ?? []
      setRules(rs)
      setSaved(rs)
    } catch (e) {
      onError((e as Error).message)
    }
  }
  useEffect(() => {
    if (online) void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  const dirty = useMemo(() => JSON.stringify(rules) !== JSON.stringify(saved), [rules, saved])

  const patch = (i: number, p: Partial<TrafficRule>) => setRules((rs) => rs.map((r, j) => (j === i ? { ...r, ...p } : r)))
  const patchParams = (i: number, p: Partial<TrafficRule['params']>) =>
    setRules((rs) => rs.map((r, j) => (j === i ? { ...r, params: { ...r.params, ...p } } : r)))
  const move = (i: number, d: -1 | 1) =>
    setRules((rs) => {
      const j = i + d
      if (j < 0 || j >= rs.length) return rs
      const next = [...rs]
      ;[next[i], next[j]] = [next[j], next[i]]
      return next
    })
  const remove = (i: number) => {
    setRules((rs) => rs.filter((_, j) => j !== i))
    setOpenIdx(null)
  }
  const add = () => {
    setRules((rs) => [...rs, blankRule()])
    setOpenIdx(rules.length)
  }

  async function save() {
    setSaving(true)
    try {
      const persisted = (await api.putTrafficRules(project.id, rules)) ?? []
      setRules(persisted)
      setSaved(persisted)
    } catch (e) {
      onError((e as Error).message) // includes the per-rule CEL/regex compile error from the server
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="trules">
      <div className="trules-head">
        <span className="muted">Checked top→bottom per phase. Modify actions fall through; <b>drop</b>/<b>hold</b> stop. Hold pauses matching traffic in the Intercept queue.</span>
        <span className="grow" />
        {dirty && <span className="trules-dirty">unsaved</span>}
        <button className="ghost-btn" onClick={add} disabled={!online}>＋ Add rule</button>
        <button className="btn-forward" onClick={save} disabled={!online || !dirty || saving}>{saving ? 'Saving…' : 'Save rules'}</button>
      </div>

      {rules.length === 0 && <div className="empty">No rules. Traffic passes through untouched. Add a rule to hold, drop, or modify it.</div>}

      {rules.map((r, i) => (
        <div key={i} className={`trule ${r.enabled ? '' : 'off'} ${openIdx === i ? 'open' : ''}`}>
          <div className="trule-row" onClick={() => setOpenIdx(openIdx === i ? null : i)}>
            <span className="trorder" onClick={(e) => e.stopPropagation()}>
              <button className="mini" disabled={i === 0} onClick={() => move(i, -1)} title="Move up">▲</button>
              <button className="mini" disabled={i === rules.length - 1} onClick={() => move(i, 1)} title="Move down">▼</button>
            </span>
            <input type="checkbox" checked={r.enabled} onClick={(e) => e.stopPropagation()} onChange={(e) => patch(i, { enabled: e.target.checked })} title={r.enabled ? 'enabled' : 'disabled'} />
            <span className={`phase phase-${r.phase}`}>{r.phase === 'both' ? 'req+resp' : r.phase === 'response' ? 'resp' : 'req'}</span>
            <span className="mono trmatch">{r.match || <span className="muted">any traffic</span>}</span>
            <span className={`tract act-${actMeta(r.action).cls}`}>{actionSummary(r)}</span>
          </div>

          {openIdx === i && (
            <div className="trule-ed">
              <div className="tr-line">
                <label className="tr-lbl">Phase</label>
                <span className="seg">
                  {(['request', 'response', 'both'] as const).map((p) => (
                    <button key={p} className={r.phase === p ? 'on' : ''} onClick={() => patch(i, { phase: p })}>{p === 'both' ? 'Both' : p === 'request' ? 'Request' : 'Response'}</button>
                  ))}
                </span>
              </div>

              <label className="tr-lbl">Match — CEL condition (empty = any)</label>
              <textarea className="mono tr-match-in" value={r.match} onChange={(e) => patch(i, { match: e.target.value })} placeholder={'e.g. host.endsWith("acme.example") && content_type.contains("json")'} />
              <div className="tr-cheats">
                {['method', 'host', 'path', 'status', 'content_type', 'header["…"]', 'body', 'json.…', '.contains()', '.matches(re)', 'has(…)'].map((c) => (
                  <span key={c} className="tr-chip">{c}</span>
                ))}
              </div>

              <div className="tr-line">
                <label className="tr-lbl">Action</label>
                <select value={r.action} onChange={(e) => patch(i, { action: e.target.value as RuleAction })}>
                  {ACTIONS.map((a) => <option key={a.value} value={a.value}>{a.label}</option>)}
                </select>
              </div>

              {(r.action === 'set_header' || r.action === 'remove_header') && (
                <div className="tr-params">
                  <input className="mono" placeholder="Header name" value={r.params.header_name ?? ''} onChange={(e) => patchParams(i, { header_name: e.target.value })} />
                  {r.action === 'set_header' && (
                    <input className="mono grow" placeholder="Value" value={r.params.header_value ?? ''} onChange={(e) => patchParams(i, { header_value: e.target.value })} />
                  )}
                </div>
              )}
              {r.action === 'replace_body' && (
                <div className="tr-params">
                  <input className="mono" placeholder="Find (regex)" value={r.params.pattern ?? ''} onChange={(e) => patchParams(i, { pattern: e.target.value })} />
                  <input className="mono grow" placeholder="Replace with" value={r.params.replacement ?? ''} onChange={(e) => patchParams(i, { replacement: e.target.value })} />
                </div>
              )}
              {r.action === 'set_status' && (
                <div className="tr-params">
                  <input className="mono" style={{ width: 110 }} placeholder="Status" value={r.params.status ?? ''} onChange={(e) => patchParams(i, { status: Number(e.target.value.replace(/[^0-9]/g, '')) || 0 })} />
                </div>
              )}
              {(r.action === 'hold' || r.action === 'drop') && (
                <div className="tr-hint">{r.action === 'hold' ? 'Matching traffic pauses in the Intercept queue for manual edit → forward/drop.' : 'Matching traffic is blocked (a 403 to the client).'}</div>
              )}

              <div className="tr-ed-actions">
                <button className="del" onClick={() => remove(i)}>✕ Delete rule</button>
              </div>
            </div>
          )}
        </div>
      ))}
    </div>
  )
}
