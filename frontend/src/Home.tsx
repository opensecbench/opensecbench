import { useCallback, useEffect, useState, type FormEvent, type MouseEvent } from 'react'
import { api, HomeData, Project, Template, SearchResult } from './api'

// Compact number for token counts: 1_240_000 -> "1.2M", 310_000 -> "310k".
const fmtTokens = (n: number): string =>
  n >= 1e6 ? `${(n / 1e6).toFixed(n >= 1e7 ? 0 : 1)}M` : n >= 1e3 ? `${Math.round(n / 1e3)}k` : String(n)

// Human interval for a schedule's period: 86400 -> "1d", 3600 -> "1h".
const fmtInterval = (s: number): string =>
  s % 86400 === 0 ? `${s / 86400}d` : s % 3600 === 0 ? `${s / 3600}h` : s % 60 === 0 ? `${s / 60}m` : `${s}s`

// Relative time to a next-run timestamp: "in 2h", or "overdue" once it has passed.
const fromNow = (iso: string): string => {
  const ms = new Date(iso).getTime() - Date.now()
  if (ms < 0) return 'overdue'
  const m = Math.round(ms / 60000)
  return `in ${m < 60 ? `${m}m` : m < 1440 ? `${Math.round(m / 60)}h` : `${Math.round(m / 1440)}d`}`
}

// A small donut showing methodology coverage percent on a project card.
function Ring({ pct }: { pct: number }) {
  const r = 8
  const c = 2 * Math.PI * r
  return (
    <svg className="cov-ring" width="22" height="22" viewBox="0 0 22 22" aria-label={`${pct}% covered`}>
      <circle className="cov-track" cx="11" cy="11" r={r} />
      <circle className="cov-fill" cx="11" cy="11" r={r} strokeDasharray={`${(c * pct) / 100} ${c}`} transform="rotate(-90 11 11)" />
    </svg>
  )
}

export function Home({ online, onOpen }: { online: boolean; onOpen: (p: Project, target?: { surface?: string; thread?: string }) => void }) {
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
  const runningTasks = home?.active.tasks ?? []
  const usage = home?.usage
  const schedules = home?.schedules ?? []
  const monthTokens = usage ? usage.month_input + usage.month_output : 0
  const allTokens = usage ? usage.all_input + usage.all_output : 0
  const open = (id: string | undefined, target?: { surface?: string; thread?: string }) => {
    const p = projects.find((x) => x.id === id)
    if (p) onOpen(p, target)
  }

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
                <li key={a.id} className="mc-row link" onClick={() => open(a.project_id, { thread: a.thread_id })}>
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
            {(activeThreads.length > 0 || runningTasks.length > 0) && <span className="mc-pill">{activeThreads.length + runningTasks.length}</span>}
          </div>
          {activeThreads.length === 0 && runningTasks.length === 0 ? (
            <div className="mc-empty">Nothing running.</div>
          ) : (
            <ul className="mc-list">
              {runningTasks.map((t) => (
                <li key={t.id} className={`mc-row ${t.project_id ? 'link' : ''}`} onClick={() => t.project_id && open(t.project_id, { surface: 'tasks' })}>
                  <span>🧪</span>
                  <span>{t.capability}</span>
                  <span className="grow" />
                  <span className="muted">{t.project || 'no project'}</span>
                </li>
              ))}
              {activeThreads.map((t) => (
                <li key={t.id} className={`mc-row ${t.project_id ? 'link' : ''}`} onClick={() => t.project_id && open(t.project_id, { thread: t.id })}>
                  <span className={`orch-dot s-${t.status === 'awaiting_approval' ? 'running' : 'done'}`} />
                  <span>{t.agent_type}</span>
                  <span className="grow" />
                  <span className="muted">{t.project || t.title}</span>
                </li>
              ))}
            </ul>
          )}
        </section>

        {/* Token spend (informational — no cap) */}
        <section className="mc-card">
          <div className="mc-head">◔ Token spend</div>
          {!usage || allTokens === 0 ? (
            <div className="mc-empty">No model usage recorded yet.</div>
          ) : (
            <div className="mc-spend">
              <div className="spend-totals">
                <div className="spend-fig">
                  <span className="spend-num">{fmtTokens(monthTokens)}</span>
                  <span className="spend-lbl">this month</span>
                </div>
                <div className="spend-fig">
                  <span className="spend-num">{fmtTokens(allTokens)}</span>
                  <span className="spend-lbl">all time</span>
                </div>
              </div>
              {usage.top_models.length > 0 && (
                <ul className="spend-bars">
                  {usage.top_models.map((m) => {
                    const tot = m.input_tokens + m.output_tokens
                    const top = usage.top_models[0].input_tokens + usage.top_models[0].output_tokens
                    return (
                      <li key={m.provider + m.model} className="spend-bar">
                        <span className="spend-model">{m.model || m.provider}</span>
                        <span className="spend-track"><span className="spend-val" style={{ width: `${top > 0 ? (tot * 100) / top : 0}%` }} /></span>
                        <span className="muted spend-amt">{fmtTokens(tot)}</span>
                      </li>
                    )
                  })}
                </ul>
              )}
            </div>
          )}
        </section>

        {/* Triggers & watchers: scheduled playbook runs */}
        <section className="mc-card">
          <div className="mc-head">
            ⟳ Triggers {schedules.length > 0 && <span className="mc-pill">{schedules.length}</span>}
          </div>
          {schedules.length === 0 ? (
            <div className="mc-empty">No scheduled runs. Set one up in a project's Agents view.</div>
          ) : (
            <ul className="mc-list">
              {schedules.map((sc) => (
                <li key={sc.id} className="mc-row link" onClick={() => open(sc.project_id, { surface: 'orchestrate' })}>
                  <span className={`orch-dot s-${sc.enabled ? 'done' : 'skipped'}`} />
                  <span>{sc.playbook}</span>
                  <span className="muted">· {sc.project}</span>
                  <span className="grow" />
                  <span className="muted">{sc.enabled ? `every ${fmtInterval(sc.interval_seconds)} · ${fromNow(sc.next_run_at)}` : 'paused'}</span>
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
                  {c && c.adopted > 0 && (
                    <div className="card-cov" title={`${c.covered_pct}% methodology coverage`}>
                      <Ring pct={c.covered_pct} />
                      <span className="cov-pct">{c.covered_pct}%</span>
                    </div>
                  )}
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
