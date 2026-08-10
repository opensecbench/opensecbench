import { useEffect, useState, type KeyboardEvent } from 'react'
import { api, Project, ResearchItem } from './api'

const TYPES = ['note', 'hypothesis', 'lead', 'question', 'experiment', 'result', 'conclusion'] as const
const STATUSES = ['open', 'active', 'resolved', 'discarded'] as const
const ASSESSMENTS = ['', 'low', 'medium', 'high', 'confirmed'] as const

const TYPE_COLORS: Record<string, string> = {
  note: '#888', hypothesis: '#4a9eff', lead: '#d4a017', question: '#9b59b6',
  experiment: '#e67e22', result: '#27ae60', conclusion: '#16a085',
}

function relTime(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime()
  if (ms < 60_000) return 'just now'
  if (ms < 3_600_000) return Math.floor(ms / 60_000) + 'm ago'
  if (ms < 86_400_000) return Math.floor(ms / 3_600_000) + 'h ago'
  return new Date(iso).toLocaleDateString()
}

export function ResearchTab({ project, online, onError }: { project: Project; online: boolean; onError: (m: string) => void }) {
  const [items, setItems] = useState<ResearchItem[]>([])
  const [newText, setNewText] = useState('')
  const [expanded, setExpanded] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = () => {
    if (!online) return
    api.listResearch(project.id).then((r) => setItems((r ?? []).sort((a, b) => b.created_at.localeCompare(a.created_at)))).catch((e) => onError((e as Error).message))
  }
  useEffect(load, [online, project.id, onError])

  async function quickCapture() {
    const text = newText.trim()
    if (!text) return
    setBusy(true)
    try {
      await api.createResearch(project.id, { type: 'note', title: text })
      setNewText('')
      load()
    } catch (e) { onError((e as Error).message) }
    finally { setBusy(false) }
  }

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => { if (e.key === 'Enter' && !busy) void quickCapture() }

  return (
    <div className="content">
      <div className="hero"><h1>Research</h1><p>Investigation notes, hypotheses, and experiments.</p></div>

      <div className="ri-capture">
        <input className="em-in ri-input" value={newText} onChange={(e) => setNewText(e.target.value)} onKeyDown={onKey}
          placeholder="Quick note — press Enter" disabled={!online || busy} />
      </div>

      {items.length === 0 && <div className="empty">{online ? 'No research items yet.' : 'Offline.'}</div>}

      {items.map((item) => (
        <ResearchCard key={item.id} item={item} expanded={expanded === item.id}
          onToggle={() => setExpanded(expanded === item.id ? null : item.id)}
          onSaved={load} onError={onError} online={online} />
      ))}
    </div>
  )
}

function ResearchCard({ item, expanded, onToggle, onSaved, onError, online }: {
  item: ResearchItem; expanded: boolean; onToggle: () => void
  onSaved: () => void; onError: (m: string) => void; online: boolean
}) {
  const [title, setTitle] = useState(item.title)
  const [body, setBody] = useState(item.body ?? '')
  const [type, setType] = useState(item.type)
  const [status, setStatus] = useState(item.status)
  const [assessment, setAssessment] = useState(item.assessment ?? '')
  const [tagsText, setTagsText] = useState((item.tags ?? []).join(', '))
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setTitle(item.title)
    setBody(item.body ?? '')
    setType(item.type)
    setStatus(item.status)
    setAssessment(item.assessment ?? '')
    setTagsText((item.tags ?? []).join(', '))
  }, [item])

  async function save() {
    setSaving(true)
    try {
      const tags = tagsText.split(',').map((t) => t.trim()).filter(Boolean)
      await api.updateResearch(item.id, { title, body, status, assessment: assessment || undefined, tags })
      onSaved()
    } catch (e) { onError((e as Error).message) }
    finally { setSaving(false) }
  }

  async function remove() {
    try {
      await api.deleteResearch(item.id)
      onSaved()
    } catch (e) { onError((e as Error).message) }
  }

  const color = TYPE_COLORS[item.type] ?? '#888'

  return (
    <section className="panel ri-card" onClick={expanded ? undefined : onToggle} style={{ cursor: expanded ? 'default' : 'pointer' }}>
      <div className="ri-header">
        <span className="ri-type" style={{ background: color }}>{item.type}</span>
        <span className="ri-title">{item.title}</span>
        <span className={`ri-status ri-status-${item.status}`}>{item.status}</span>
        {item.assessment && <span className="ri-assessment">{item.assessment}</span>}
        {(item.tags ?? []).map((t) => <span key={t} className="ri-tag">{t}</span>)}
        <span className="ri-time">{relTime(item.created_at)}</span>
        {item.created_by !== 'manual' && <span className="ri-by">{item.created_by}</span>}
      </div>

      {expanded && (
        <div className="ri-body" onClick={(e) => e.stopPropagation()}>
          <div className="em-field"><label>Title</label><input className="em-in" value={title} onChange={(e) => setTitle(e.target.value)} /></div>
          <div className="em-two">
            <div className="em-field"><label>Type</label>
              <select className="em-in" value={type} onChange={(e) => setType(e.target.value)}>
                {TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
              </select>
            </div>
            <div className="em-field"><label>Status</label>
              <select className="em-in" value={status} onChange={(e) => setStatus(e.target.value)}>
                {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
              </select>
            </div>
          </div>
          <div className="em-two">
            <div className="em-field"><label>Assessment</label>
              <select className="em-in" value={assessment} onChange={(e) => setAssessment(e.target.value)}>
                {ASSESSMENTS.map((a) => <option key={a} value={a}>{a || '— None —'}</option>)}
              </select>
            </div>
            <div className="em-field"><label>Tags <span className="em-opt">comma-separated</span></label>
              <input className="em-in" value={tagsText} onChange={(e) => setTagsText(e.target.value)} />
            </div>
          </div>
          <div className="em-field"><label>Body</label><textarea className="em-in" rows={4} value={body} onChange={(e) => setBody(e.target.value)} /></div>
          <div className="ri-actions">
            <button className="pbuild-save" disabled={!online || saving} onClick={save}>{saving ? 'Saving…' : 'Save'}</button>
            <button className="em-btn ri-delete" onClick={remove}>Delete</button>
            <button className="em-btn" onClick={onToggle}>Collapse</button>
          </div>
        </div>
      )}
    </section>
  )
}
