import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { api, KBEntry, Project } from './api'
import { Markdown } from './Markdown'

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

type GroupBy = 'kind' | 'scope' | 'review' | 'none'

// Canonical ordering per grouping axis; keys not listed here fall to the end alphabetically.
const GROUP_ORDER: Record<GroupBy, string[]> = {
  kind: KINDS,
  scope: ['target', 'team', 'org', 'global'],
  review: ['unreviewed', 'confirmed', 'rejected'],
  none: ['all'],
}

const REVIEW_FILTERS = [
  { key: 'all', label: 'All' },
  { key: 'unreviewed', label: 'Unreviewed' },
  { key: 'confirmed', label: 'Confirmed' },
  { key: 'rejected', label: 'Rejected' },
]

function groupKeyOf(e: KBEntry, by: GroupBy): string {
  if (by === 'kind') return e.kind
  if (by === 'scope') return scopeLabel(e)
  if (by === 'review') return e.review_state
  return 'all'
}

function groupTitle(key: string, by: GroupBy): string {
  if (by === 'none') return 'All entries'
  if (by === 'kind') return key.replace(/_/g, ' ')
  return key.charAt(0).toUpperCase() + key.slice(1)
}

function matchesSearch(e: KBEntry, q: string): boolean {
  if (!q) return true
  return `${e.title} ${e.body ?? ''} ${e.tags ?? ''} ${e.kind}`.toLowerCase().includes(q)
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
  const [adding, setAdding] = useState(false)
  // Inline edit of an existing entry.
  const [editId, setEditId] = useState<string | null>(null)
  const [editTitle, setEditTitle] = useState('')
  const [editBody, setEditBody] = useState('')
  const [editTags, setEditTags] = useState('')
  // Catalog controls.
  const [search, setSearch] = useState('')
  const [reviewFilter, setReviewFilter] = useState('all')
  const [groupBy, setGroupBy] = useState<GroupBy>('kind')
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())

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
      setAdding(false)
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
    setEditTags(e.tags ?? '')
  }
  async function saveEdit() {
    if (!editId || !editTitle.trim()) return
    try {
      await api.updateKBEntry(editId, { title: editTitle.trim(), body: editBody, tags: editTags.trim() || undefined })
      setEditId(null)
      await reload()
    } catch (err) {
      onError((err as Error).message)
    }
  }

  function toggleGroup(key: string) {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const q = search.trim().toLowerCase()

  // Search first (drives the chip counts), then apply the review-state chip.
  const searchMatched = useMemo(() => entries.filter((e) => matchesSearch(e, q)), [entries, q])
  const reviewCounts = useMemo(() => {
    const c: Record<string, number> = { all: searchMatched.length, unreviewed: 0, confirmed: 0, rejected: 0 }
    for (const e of searchMatched) c[e.review_state] = (c[e.review_state] ?? 0) + 1
    return c
  }, [searchMatched])
  const filtered = useMemo(
    () => searchMatched.filter((e) => reviewFilter === 'all' || e.review_state === reviewFilter),
    [searchMatched, reviewFilter],
  )

  // Bucket the filtered entries by the chosen axis, in canonical order, dropping empty buckets.
  const groups = useMemo(() => {
    const byKey = new Map<string, KBEntry[]>()
    for (const e of filtered) {
      const k = groupKeyOf(e, groupBy)
      const arr = byKey.get(k)
      if (arr) arr.push(e)
      else byKey.set(k, [e])
    }
    const order = GROUP_ORDER[groupBy]
    const keys = [...byKey.keys()].sort((a, b) => {
      const ia = order.indexOf(a)
      const ib = order.indexOf(b)
      if (ia !== -1 && ib !== -1) return ia - ib
      if (ia !== -1) return -1
      if (ib !== -1) return 1
      return a.localeCompare(b)
    })
    return keys.map((k) => ({ key: k, entries: byKey.get(k)! }))
  }, [filtered, groupBy])

  return (
    <section className="panel">
      <div className="panel-head">
        Knowledge base
        <span className="grow" />
        <button className="ghost-btn" onClick={() => setAdding((v) => !v)} disabled={!online}>
          {adding ? '× Cancel' : '+ Add entry'}
        </button>
      </div>
      <p className="hint">
        Durable knowledge about the system — it persists across engagements and is inherited by the target's,
        team's, and organization's projects. AI-drafted entries stay unreviewed until you confirm them.
      </p>

      {adding && (
        <form className="kb-add" onSubmit={add}>
          <div className="replay-line">
            <select value={kind} onChange={(e) => setKind(e.target.value)}>
              {KINDS.map((k) => (
                <option key={k} value={k}>{k.replace(/_/g, ' ')}</option>
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
              autoFocus
            />
            <button type="submit" disabled={!online || busy || !title.trim()}>Add</button>
          </div>
          <textarea
            className="mono kb-editor"
            rows={6}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Details (optional) — markdown, notes, request/response snippets…"
          />
        </form>
      )}

      {entries.length === 0 ? (
        <div className="empty">No knowledge captured yet.</div>
      ) : (
        <>
          <div className="table-toolbar">
            <input
              className="tt-search"
              placeholder="Search title, details, tags…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <div className="tt-chips">
              {REVIEW_FILTERS.map((r) => (
                <button
                  key={r.key}
                  className={`chip ${reviewFilter === r.key ? 'on' : ''}`}
                  onClick={() => setReviewFilter(r.key)}
                >
                  {r.label} <span className="n">{reviewCounts[r.key] ?? 0}</span>
                </button>
              ))}
            </div>
            <span className="grow" />
            <label className="kb-groupby">
              Group by
              <select value={groupBy} onChange={(e) => setGroupBy(e.target.value as GroupBy)}>
                <option value="kind">Kind</option>
                <option value="scope">Scope</option>
                <option value="review">Status</option>
                <option value="none">Flat</option>
              </select>
            </label>
            <span className="muted tt-count">{filtered.length} shown</span>
          </div>

          {filtered.length === 0 ? (
            <div className="empty">No entries match.</div>
          ) : (
            groups.map((g) => {
              const gkey = `${groupBy}:${g.key}`
              const isCollapsed = collapsed.has(gkey)
              return (
                <div key={gkey} className="kb-group">
                  {groupBy !== 'none' && (
                    <button className="kb-group-head" onClick={() => toggleGroup(gkey)}>
                      <span className="kb-caret">{isCollapsed ? '▸' : '▾'}</span>
                      <span className="kb-group-title">{groupTitle(g.key, groupBy)}</span>
                      <span className="kb-group-n">{g.entries.length}</span>
                    </button>
                  )}
                  {!isCollapsed && (
                    <ul className="rows kb-list">
                      {g.entries.map((e) => (
                        <li key={e.id} className={`kb-item review-${e.review_state}`}>
                          {editId === e.id ? (
                            <div className="kb-main kb-edit">
                              <input className="replay-url" value={editTitle} onChange={(ev) => setEditTitle(ev.target.value)} />
                              <textarea
                                className="mono kb-editor"
                                rows={12}
                                value={editBody}
                                onChange={(ev) => setEditBody(ev.target.value)}
                                placeholder="Details — markdown, notes, request/response snippets…"
                              />
                              <input
                                className="replay-url"
                                value={editTags}
                                onChange={(ev) => setEditTags(ev.target.value)}
                                placeholder="tags (comma-separated)"
                              />
                              <div className="kb-actions">
                                <button className="link" onClick={saveEdit} disabled={!editTitle.trim()}>save</button>
                                <button className="link" onClick={() => setEditId(null)}>cancel</button>
                              </div>
                            </div>
                          ) : (
                            <>
                              <div className="kb-main">
                                <div className="kb-line">
                                  {groupBy !== 'kind' && <span className="badge">{e.kind}</span>}
                                  {groupBy !== 'scope' && <span className="badge scope">{scopeLabel(e)}</span>}
                                  {e.origin === 'thread' && <span className="badge ai">AI</span>}
                                  <span className="kb-title">{e.title}</span>
                                </div>
                                {e.body && <div className="kb-body"><Markdown source={e.body} /></div>}
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
                </div>
              )
            })
          )}
        </>
      )}
    </section>
  )
}
