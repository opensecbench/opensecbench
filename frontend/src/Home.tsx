import { useEffect, useState, type FormEvent, type MouseEvent } from 'react'
import { api, Project, Template, SearchResult } from './api'

export function Home({ online, onOpen }: { online: boolean; onOpen: (p: Project) => void }) {
  const [projects, setProjects] = useState<Project[]>([])
  const [templates, setTemplates] = useState<Template[]>([])
  const [name, setName] = useState('')
  const [template, setTemplate] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<SearchResult[] | null>(null)

  async function refresh() {
    try {
      setProjects((await api.listProjects()) ?? [])
      setTemplates((await api.listTemplates()) ?? [])
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  useEffect(() => {
    if (online) void refresh()
  }, [online])

  async function create(e: FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setBusy(true)
    try {
      if (template) await api.createProjectFromTemplate(template, name.trim())
      else await api.createProject(name.trim())
      setName('')
      setTemplate('')
      await refresh()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function remove(p: Project, e: MouseEvent) {
    e.stopPropagation()
    setBusy(true)
    try {
      await api.deleteProject(p.id)
      await refresh()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function runSearch(e: FormEvent) {
    e.preventDefault()
    if (!query.trim()) {
      setResults(null)
      return
    }
    try {
      setResults((await api.search(query.trim())) ?? [])
    } catch (err) {
      setError((err as Error).message)
    }
  }

  return (
    <div className="content">
      <div className="hero">
        <h1>Assessment workbench</h1>
        <p>{projects.length === 0 ? 'No projects yet — create one to get started.' : `${projects.length} project${projects.length === 1 ? '' : 's'}.`}</p>
      </div>

      {error && <div className="banner error">⚠ {error}</div>}

      <form className="search-bar" onSubmit={runSearch}>
        <span>⌕</span>
        <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search projects, assets, findings, observations, context…" disabled={!online} />
        {results !== null && (
          <button type="button" className="ghost-btn" onClick={() => { setResults(null); setQuery('') }}>clear</button>
        )}
      </form>

      {results !== null && (
        <section className="panel">
          <div className="panel-head">Search results ({results.length})</div>
          {results.length === 0 ? (
            <div className="empty">No matches.</div>
          ) : (
            <ul className="rows">
              {results.map((r) => (
                <li key={r.kind + r.id} className="row-item">
                  <span className={`kind kind-${r.kind}`}>{r.kind}</span>
                  <span className="row-title">{r.title}</span>
                  <span className="muted">{r.detail}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      <section className="panel">
        <div className="panel-head">Projects</div>
        <form className="create-row" onSubmit={create}>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="New project name…" disabled={!online || busy} />
          <select value={template} onChange={(e) => setTemplate(e.target.value)} disabled={!online || busy}>
            <option value="">Blank (no template)</option>
            {templates.filter((t) => t.id !== 'blank').map((t) => (
              <option key={t.id} value={t.id}>{t.name}</option>
            ))}
          </select>
          <button type="submit" disabled={!online || busy || !name.trim()}>＋ Create</button>
        </form>

        {projects.length === 0 ? (
          <div className="empty">Nothing here yet.</div>
        ) : (
          <ul className="cards">
            {projects.map((p) => (
              <li key={p.id} className="card" onClick={() => onOpen(p)}>
                <div className="card-main">
                  <div className="card-name">{p.name}</div>
                  <div className="card-meta">
                    <span className={`badge ${p.status}`}>{p.status}</span>
                    <span className="muted">created {new Date(p.created_at).toLocaleString()}</span>
                  </div>
                </div>
                <button className="del" title="Delete project" onClick={(e) => remove(p, e)}>✕</button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}
