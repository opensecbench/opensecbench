import { useEffect, useState } from 'react'
import { api, ProjectConnector, Project } from './api'

// IntegrationsTab binds this project to global connectors (ADR-0027 / IA declutter). Connectors (tracker
// instance + credential) are built once in the Library; here you attach one to the project, set its
// project-side scope, and pull findings in.
export function IntegrationsTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [connectors, setConnectors] = useState<ProjectConnector[]>([])
  const [keys, setKeys] = useState<Record<string, string>>({}) // editable project_key per connector
  const [busy, setBusy] = useState('')
  const [result, setResult] = useState<string | null>(null)

  async function load() {
    const pi = await api.getProjectIntegrations(project.id)
    const cs = pi.connectors ?? []
    setConnectors(cs)
    setKeys(Object.fromEntries(cs.map((c) => [c.id, c.project_key])))
  }
  useEffect(() => {
    if (!online) return
    void load().catch((e) => onError((e as Error).message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  async function bind(c: ProjectConnector) {
    setBusy(c.id)
    try {
      await api.setBinding(project.id, c.id, keys[c.id] ?? '')
      await load()
      setResult(`Bound ${c.name}.`)
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy('')
    }
  }
  async function unbind(c: ProjectConnector) {
    try {
      await api.deleteBinding(project.id, c.id)
      await load()
    } catch (e) {
      onError((e as Error).message)
    }
  }
  async function pull(c: ProjectConnector) {
    setBusy(c.id)
    setResult(null)
    try {
      const r = await api.pullIntegration(project.id, c.id)
      setResult(`Pulled ${c.name}: ${r.imported} imported, ${r.skipped} already present (of ${r.total}). Imported findings are unreviewed observations — triage them in Findings.`)
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  return (
    <div className="content">
      <div className="hero">
        <h1>Integrations</h1>
        <p>Attach a connector to this project and pull findings in. Connectors (tracker + credential) are defined once in the Library (📚 → Connectors).</p>
      </div>

      {result && <div className="banner">{result}</div>}

      <section className="panel">
        <div className="panel-head">Connectors</div>
        {connectors.length === 0 ? (
          <div className="empty">No connectors defined. Add one in the Library → Connectors, then bind it here.</div>
        ) : (
          <ul className="rows">
            {connectors.map((c) => (
              <li key={c.id} className={`row-item ${c.bound ? 'bound' : ''}`}>
                <span className="kind">{c.type}</span>
                <span className="row-title">{c.name}{c.bound ? '' : ' — not bound'}</span>
                <input
                  className="integ-key"
                  placeholder="project key / test id"
                  value={keys[c.id] ?? ''}
                  onChange={(e) => setKeys((k) => ({ ...k, [c.id]: e.target.value }))}
                />
                <span className="grow" />
                <button className="mini" disabled={!online || busy === c.id} onClick={() => bind(c)}>{c.bound ? 'update' : 'bind'}</button>
                {c.bound && c.pullable && (
                  <button className="mini ok" disabled={!online || busy === c.id} onClick={() => pull(c)}>{busy === c.id ? '…' : '⟱ Pull'}</button>
                )}
                {c.bound && <button className="mini no" onClick={() => unbind(c)}>unbind</button>}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}
