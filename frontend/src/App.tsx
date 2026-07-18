import { useEffect, useState } from 'react'
import { api, Project } from './api'

type Conn = 'connecting' | 'online' | 'offline'

export function App() {
  const [conn, setConn] = useState<Conn>('connecting')
  const [projects, setProjects] = useState<Project[]>([])
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [busy, setBusy] = useState(false)
  const [selected, setSelected] = useState<Project | null>(null)

  async function refresh() {
    try {
      await api.health()
      setConn('online')
      setProjects((await api.listProjects()) ?? [])
      setError(null)
    } catch (e) {
      setConn('offline')
      setError((e as Error).message)
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  async function createProject(e: React.FormEvent) {
    e.preventDefault()
    const name = newName.trim()
    if (!name) return
    setBusy(true)
    try {
      await api.createProject(name)
      setNewName('')
      await refresh()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function remove(p: Project) {
    setBusy(true)
    try {
      await api.deleteProject(p.id)
      if (selected?.id === p.id) setSelected(null)
      await refresh()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="app">
      <aside className="rail">
        <div className="logo" title="OpenSecBench" />
        <button className="rail-btn active" title="Home">
          ⌂
        </button>
      </aside>

      <main className="main">
        <header className="topbar">
          <div className="crumb">Home</div>
          <div className="spacer" />
          <span className={`conn conn-${conn}`}>
            <i /> {conn === 'online' ? 'control plane online' : conn === 'offline' ? 'control plane offline' : 'connecting…'}
          </span>
          <code className="apiurl">{api.baseURL}</code>
        </header>

        <div className="content">
          <div className="hero">
            <h1>Assessment workbench</h1>
            <p>
              {projects.length === 0
                ? 'No projects yet — create one to get started.'
                : `${projects.length} project${projects.length === 1 ? '' : 's'}.`}
            </p>
          </div>

          {error && <div className="banner error">⚠ {error}</div>}

          <section className="panel">
            <div className="panel-head">
              <span>Projects</span>
            </div>

            <form className="create-row" onSubmit={createProject}>
              <input
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="New project name…"
                disabled={conn !== 'online' || busy}
              />
              <button type="submit" disabled={conn !== 'online' || busy || !newName.trim()}>
                ＋ Create
              </button>
            </form>

            {projects.length === 0 ? (
              <div className="empty">Nothing here yet.</div>
            ) : (
              <ul className="cards">
                {projects.map((p) => (
                  <li
                    key={p.id}
                    className={`card ${selected?.id === p.id ? 'sel' : ''}`}
                    onClick={() => setSelected(p)}
                  >
                    <div className="card-main">
                      <div className="card-name">{p.name}</div>
                      <div className="card-meta">
                        <span className={`badge ${p.status}`}>{p.status}</span>
                        <span className="muted">created {new Date(p.created_at).toLocaleString()}</span>
                      </div>
                    </div>
                    <button
                      className="del"
                      title="Delete project"
                      onClick={(e) => {
                        e.stopPropagation()
                        void remove(p)
                      }}
                    >
                      ✕
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
      </main>

      {selected && (
        <aside className="workbench">
          <div className="wb-head">
            <div className="wb-title">{selected.name}</div>
            <button className="wb-close" onClick={() => setSelected(null)}>
              ✕
            </button>
          </div>
          <div className="wb-tabs">
            {['Overview', 'Methodology', 'Evidence', 'Findings'].map((t, i) => (
              <span key={t} className={`wb-tab ${i === 0 ? 'on' : 'disabled'}`}>
                {t}
              </span>
            ))}
          </div>
          <div className="wb-body">
            <dl className="kv">
              <dt>ID</dt>
              <dd>
                <code>{selected.id}</code>
              </dd>
              <dt>Status</dt>
              <dd>{selected.status}</dd>
              <dt>Targets</dt>
              <dd>{selected.target_ids?.length ?? 0}</dd>
            </dl>
            <p className="soon">Workbench surfaces (methodology, evidence, findings, the Analyst) arrive in later phases.</p>
          </div>
        </aside>
      )}
    </div>
  )
}
