import { useEffect, useState } from 'react'
import { api, IntegrationConfig, Project } from './api'

// Per-project integrations (ADR-0027): configure a tracker once (reused by push + pull), and pull
// external findings in as observations that enter triage.
export function IntegrationsTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [configs, setConfigs] = useState<IntegrationConfig[]>([])
  const [connectors, setConnectors] = useState<{ name: string; pullable: boolean }[]>([])
  const [secrets, setSecrets] = useState<string[]>([])
  const [integration, setIntegration] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [projectKey, setProjectKey] = useState('')
  const [credential, setCredential] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<string | null>(null)

  async function load() {
    const pi = await api.getProjectIntegrations(project.id)
    setConfigs(pi.configs ?? [])
    setConnectors(pi.connectors ?? [])
    if (!integration && pi.connectors?.length) setIntegration(pi.connectors[0].name)
  }

  useEffect(() => {
    if (!online) return
    void load().catch((e) => onError((e as Error).message))
    api.listSecrets().then((s) => setSecrets((s ?? []).map((x) => x.name))).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  // Prefill the form from an existing config when the selected integration changes.
  useEffect(() => {
    const c = configs.find((x) => x.integration === integration)
    setBaseUrl(c?.base_url ?? '')
    setProjectKey(c?.project_key ?? '')
    setCredential(c?.credential ?? '')
  }, [integration, configs])

  const pullable = (name: string) => connectors.find((c) => c.name === name)?.pullable ?? false

  async function save() {
    if (!integration || !baseUrl.trim()) return
    setBusy(true)
    try {
      await api.setIntegrationConfig(project.id, integration, {
        base_url: baseUrl.trim(),
        project_key: projectKey.trim(),
        credential,
      })
      await load()
      setResult('Saved.')
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function pull(name: string) {
    setBusy(true)
    setResult(null)
    try {
      const r = await api.pullIntegration(project.id, name)
      setResult(`Pulled ${name}: ${r.imported} imported, ${r.skipped} already present (of ${r.total}). Imported findings are unreviewed observations — triage them in Findings.`)
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function remove(name: string) {
    try {
      await api.deleteIntegrationConfig(project.id, name)
      await load()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  return (
    <div className="content">
      <div className="hero">
        <h1>Integrations</h1>
        <p>Connect an external tracker once — reused for pushing findings out and pulling findings in.</p>
      </div>

      {result && <div className="banner">{result}</div>}

      <section className="panel">
        <div className="panel-head">Configured</div>
        {configs.length === 0 ? (
          <div className="empty">No integrations configured yet.</div>
        ) : (
          <ul className="rows">
            {configs.map((c) => (
              <li key={c.id} className="row-item">
                <span className="kind">{c.integration}</span>
                <span className="row-title mono">{c.base_url}</span>
                <span className="muted">key {c.project_key || '—'} · cred {c.credential || '—'}</span>
                <span className="grow" />
                {pullable(c.integration) && (
                  <button className="mini ok" disabled={!online || busy} onClick={() => pull(c.integration)}>
                    ⟱ Pull
                  </button>
                )}
                <button className="mini no" disabled={busy} onClick={() => remove(c.integration)}>remove</button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="panel">
        <div className="panel-head">Configure</div>
        <div className="create-row">
          <select value={integration} onChange={(e) => setIntegration(e.target.value)}>
            {connectors.map((c) => (
              <option key={c.name} value={c.name}>{c.name}{c.pullable ? ' (push + pull)' : ' (push)'}</option>
            ))}
          </select>
          <input placeholder="base URL, e.g. https://defectdojo.local" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
          <input placeholder="project key / test id" value={projectKey} onChange={(e) => setProjectKey(e.target.value)} />
          <select value={credential} onChange={(e) => setCredential(e.target.value)}>
            <option value="">credential (vault secret)…</option>
            {secrets.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
          <button disabled={!online || busy || !integration || !baseUrl.trim()} onClick={save}>Save</button>
        </div>
        <p className="hint">The credential is a vault secret name — add it under Secrets first; its value never leaves the vault.</p>
      </section>
    </div>
  )
}
