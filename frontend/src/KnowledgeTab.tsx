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

// Human-readable scope label for a KB entry (where the knowledge lives / who inherits it).
function scopeLabel(e: KBEntry): string {
  if (e.group_id) return 'team'
  if (e.organization_id) return 'org'
  if (e.target_id) return 'target'
  return 'global'
}

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
  const [scope, setScope] = useState('target')
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  // Inline edit of an existing entry.
  const [editId, setEditId] = useState<string | null>(null)
  const [editTitle, setEditTitle] = useState('')
  const [editBody, setEditBody] = useState('')

  const targets = project.target_ids ?? []

  // Scopes offered depend on what the project is associated with (ADR-0041): target when it has one, team/org
  // when the project belongs to them, global always. Default to the narrowest available.
  const scopeChoices = [
    ...(targets.length > 0 ? [{ v: 'target', label: targets.length > 1 ? 'This target (first)' : 'This target' }] : []),
    ...(project.group_id ? [{ v: 'group', label: 'Team — shared across the team' }] : []),
    ...(project.organization_id ? [{ v: 'org', label: 'Organization — shared org-wide' }] : []),
    { v: 'global', label: 'Global — every project' },
  ]
  useEffect(() => {
    if (!scopeChoices.some((s) => s.v === scope)) setScope(scopeChoices[0]?.v ?? 'global')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project.id])

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
    if (!title.trim()) return
    setBusy(true)
    try {
      await api.createKBScoped({
        scope,
        kind,
        title: title.trim(),
        body,
        target_id: scope === 'target' ? targets[0] : undefined,
        group_id: scope === 'group' ? project.group_id : undefined,
        organization_id: scope === 'org' ? project.organization_id : undefined,
      })
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

  function startEdit(e: KBEntry) {
    setEditId(e.id)
    setEditTitle(e.title)
    setEditBody(e.body ?? '')
  }
  async function saveEdit() {
    if (!editId || !editTitle.trim()) return
    try {
      await api.updateKBEntry(editId, { title: editTitle.trim(), body: editBody })
      setEditId(null)
      await reload()
    } catch (err) {
      onError((err as Error).message)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">Knowledge base</div>
      <p className="hint">
        Durable knowledge about the system — it persists across engagements and is inherited by the target's,
        team's, and organization's projects. AI-drafted entries stay unreviewed until you confirm them.
      </p>

      <form className="kb-add" onSubmit={add}>
        <div className="replay-line">
          <select value={kind} onChange={(e) => setKind(e.target.value)}>
            {KINDS.map((k) => (
              <option key={k} value={k}>{k.replace('_', ' ')}</option>
            ))}
          </select>
          <select value={scope} onChange={(e) => setScope(e.target.value)} title="Where this knowledge lives / who inherits it">
            {scopeChoices.map((s) => (
              <option key={s.v} value={s.v}>{s.label}</option>
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

      {entries.length === 0 ? (
        <div className="empty">No knowledge captured yet.</div>
      ) : (
        <ul className="rows kb-list">
          {entries.map((e) => (
            <li key={e.id} className={`kb-item review-${e.review_state}`}>
              {editId === e.id ? (
                <div className="kb-main kb-edit">
                  <input className="replay-url" value={editTitle} onChange={(ev) => setEditTitle(ev.target.value)} />
                  <textarea className="mono" rows={3} value={editBody} onChange={(ev) => setEditBody(ev.target.value)} />
                  <div className="kb-actions">
                    <button className="link" onClick={saveEdit} disabled={!editTitle.trim()}>save</button>
                    <button className="link" onClick={() => setEditId(null)}>cancel</button>
                  </div>
                </div>
              ) : (
                <>
                  <div className="kb-main">
                    <div className="kb-line">
                      <span className="badge">{e.kind}</span>
                      <span className="badge scope">{scopeLabel(e)}</span>
                      {e.origin === 'thread' && <span className="badge ai">AI draft</span>}
                      <span className="kb-title">{e.title}</span>
                    </div>
                    {e.body && <div className="kb-body">{e.body}</div>}
                    {e.tags && <div className="mitem-std">{e.tags}</div>}
                  </div>
                  <div className="kb-actions">
                    {e.review_state === 'unreviewed' ? (
                      <>
                        <button className="link" onClick={() => review(e.id, 'confirmed')}>confirm</button>
                        <button className="link danger" onClick={() => review(e.id, 'rejected')}>reject</button>
                      </>
                    ) : (
                      <span className={`badge ${e.review_state === 'confirmed' ? 'succeeded' : 'failed'}`}>{e.review_state}</span>
                    )}
                    <button className="link" onClick={() => startEdit(e)}>edit</button>
                  </div>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
