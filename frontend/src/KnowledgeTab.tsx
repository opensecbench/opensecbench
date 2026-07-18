import { useEffect, useState, type FormEvent } from 'react'
import { api, KBEntry, Project } from './api'

const KINDS = [
  'architecture',
  'auth',
  'endpoint',
  'tech_stack',
  'environment',
  'data_flow',
  'convention',
  'gotcha',
  'tactic',
]

export function KnowledgeTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [entries, setEntries] = useState<KBEntry[]>([])
  const [kind, setKind] = useState('architecture')
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)

  const targets = project.target_ids ?? []

  async function reload() {
    try {
      setEntries((await api.listProjectKB(project.id)) ?? [])
    } catch (e) {
      onError((e as Error).message)
    }
  }

  useEffect(() => {
    if (online) void reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  async function add(e: FormEvent) {
    e.preventDefault()
    if (!title.trim() || targets.length === 0) return
    setBusy(true)
    try {
      await api.createKBEntry(targets[0], { kind, title: title.trim(), body })
      setTitle('')
      setBody('')
      await reload()
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function review(id: string, state: string) {
    try {
      await api.reviewKBEntry(id, state)
      await reload()
    } catch (err) {
      onError((err as Error).message)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">Knowledge base</div>
      <p className="hint">
        Durable knowledge about the target — it persists across engagements. AI-drafted entries are
        marked and stay unreviewed until you confirm them.
      </p>

      {targets.length === 0 ? (
        <div className="banner">Link a target to this project to build a knowledge base.</div>
      ) : (
        <form className="kb-add" onSubmit={add}>
          <div className="replay-line">
            <select value={kind} onChange={(e) => setKind(e.target.value)}>
              {KINDS.map((k) => (
                <option key={k} value={k}>{k.replace('_', ' ')}</option>
              ))}
            </select>
            <input
              className="replay-url"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="e.g. Auth is SAML SSO via Okta"
              disabled={!online || busy}
            />
            <button type="submit" disabled={!online || busy || !title.trim()}>Add</button>
          </div>
          <textarea
            className="mono"
            rows={3}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Details (optional)"
          />
        </form>
      )}

      {entries.length === 0 ? (
        <div className="empty">No knowledge captured yet.</div>
      ) : (
        <ul className="rows kb-list">
          {entries.map((e) => (
            <li key={e.id} className={`kb-item review-${e.review_state}`}>
              <div className="kb-main">
                <div className="kb-line">
                  <span className="badge">{e.kind}</span>
                  {e.origin === 'thread' && <span className="badge ai">AI draft</span>}
                  <span className="kb-title">{e.title}</span>
                </div>
                {e.body && <div className="kb-body">{e.body}</div>}
                {e.tags && <div className="mitem-std">{e.tags}</div>}
              </div>
              {e.review_state === 'unreviewed' ? (
                <div className="kb-actions">
                  <button className="link" onClick={() => review(e.id, 'confirmed')}>confirm</button>
                  <button className="link danger" onClick={() => review(e.id, 'rejected')}>reject</button>
                </div>
              ) : (
                <span className={`badge ${e.review_state === 'confirmed' ? 'succeeded' : 'failed'}`}>{e.review_state}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
