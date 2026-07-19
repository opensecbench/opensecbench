import { useCallback, useEffect, useState, type FormEvent, type MouseEvent } from 'react'
import { api, HomeData, Project, Template, SearchResult } from './api'

export function Home({ online, onOpen }: { online: boolean; onOpen: (p: Project) => void }) {
  const [projects, setProjects] = useState<Project[]>([])
  const [templates, setTemplates] = useState<Template[]>([])
  const [home, setHome] = useState<HomeData | null>(null)
  const [name, setName] = useState('')
  const [template, setTemplate] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<SearchResult[] | null>(null)

  const loadHome = useCallback(async () => {
    try {
      setHome(await api.getHome())
    } catch {
      /* offline / transient */
    }
  }, [])

  const refresh = useCallback(async () => {
    try {
      setProjects((await api.listProjects()) ?? [])
      setTemplates((await api.listTemplates()) ?? [])
      setError(null)
      await loadHome()
    } catch (e) {
      setError((e as Error).message)
    }
  }, [loadHome])

  useEffect(() => {
    if (!online) return
    void refresh()
    const t = setInterval(loadHome, 5000) // keep "waiting on you" / "running now" live
    return () => clearInterval(t)
  }, [online, refresh, loadHome])

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

  const counts = new Map((home?.projects ?? []).map((p) => [p.id, p]))
  const approvals = home?.approvals ?? []
  const activeThreads = home?.active.threads ?? []
  const runningTasks = home?.active.running_tasks ?? 0
  const openById = (id?: string) => id && onOpen(projects.find((p) => p.id === id) as Project)

  return (
    <div className="content wide">
      <div className="hero">
        <h1>Mission control</h1>
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

      <div className="mc-grid">
        {/* Waiting on you */}
        <section className="mc-card">
          <div className="mc-head">⏸ Waiting on you {approvals.length > 0 && <span className="mc-pill amber">{approvals.length}</span>}</div>
          {approvals.length === 0 ? (
            <div className="mc-empty">Nothing needs approval.</div>
          ) : (
            <ul className="mc-list">
              {approvals.map((a) => (
                <li key={a.id} className="mc-row" onClick={() => openById(a.project_id)}>
                  <code>{a.tool}</code>
                  <span className="grow" />
                  <span className="muted">{a.project || 'no project'}</span>
                </li>
              ))}
            </ul>
          )}
        </section>

        {/* Running now */}
        <section className="mc-card">
          <div className="mc-head">
            ▷ Running now
            {(activeThreads.length > 0 || runningTasks > 0) && <span className="mc-pill">{activeThreads.length + runningTasks}</span>}
          </div>
          {activeThreads.length === 0 && runningTasks === 0 ? (
            <div className="mc-empty">Nothing running.</div>
          ) : (
            <ul className="mc-list">
              {runningTasks > 0 && (
                <li className="mc-row"><span>🧪 {runningTasks} capability task{runningTasks === 1 ? '' : 's'} running</span></li>
              )}
              {activeThreads.map((t) => (
                <li key={t.id} className="mc-row">
                  <span className={`orch-dot s-${t.status === 'awaiting_approval' ? 'running' : 'done'}`} />
                  <span>{t.agent_type}</span>
                  <span className="grow" />
                  <span className="muted">{t.project || t.title}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>

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
            {projects.map((p) => {
              const c = counts.get(p.id)
              return (
                <li key={p.id} className="card" onClick={() => onOpen(p)}>
                  <div className="card-main">
                    <div className="card-name">{p.name}</div>
                    <div className="card-meta">
                      <span className={`badge ${p.status}`}>{p.status}</span>
                      {c && <span className="mc-stat">{c.findings} finding{c.findings === 1 ? '' : 's'}</span>}
                      {c && c.high > 0 && <span className="mc-stat red">{c.high} high+</span>}
                      {c && c.to_triage > 0 && <span className="mc-stat amber">{c.to_triage} to triage</span>}
                    </div>
                  </div>
                  <button className="del" title="Delete project" onClick={(e) => remove(p, e)}>✕</button>
                </li>
              )
            })}
          </ul>
        )}
      </section>
    </div>
  )
}
