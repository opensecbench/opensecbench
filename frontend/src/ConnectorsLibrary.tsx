import { useEffect, useState } from 'react'
import { api, Connector, ConnectorType } from './api'

// ConnectorsLibrary manages global external-tracker connectors (ADR-0027 / IA declutter): a tracker
// instance + credential, built once and bound to projects from their Integrations surface. Lives under
// Settings ▸ Connectors (moved from Library so all instance-level configuration sits together).
export function ConnectorsLibrary({ online }: { online: boolean }) {
  const [connectors, setConnectors] = useState<Connector[]>([])
  const [types, setTypes] = useState<ConnectorType[]>([])
  const [secrets, setSecrets] = useState<string[]>([])
  const [name, setName] = useState('')
  const [type, setType] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [credential, setCredential] = useState('')
  const [error, setError] = useState<string | null>(null)

  async function load() {
    try {
      setConnectors((await api.listConnectors()) ?? [])
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    if (!online) return
    void load()
    api.listConnectorTypes().then((t) => { setTypes(t ?? []); if (t?.length) setType((prev) => prev || t[0].type) }).catch(() => {})
    api.listSecrets().then((s) => setSecrets((s ?? []).map((x) => x.name))).catch(() => {})
  }, [online])

  async function add() {
    if (!name.trim() || !type || !baseUrl.trim()) return
    try {
      await api.createConnector({ name: name.trim(), type, base_url: baseUrl.trim(), credential })
      setName(''); setBaseUrl(''); setCredential('')
      setError(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }
  async function del(id: string) {
    try {
      await api.deleteConnector(id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const pullable = (t: string) => types.find((x) => x.type === t)?.pullable ?? false

  return (
    <div className="lib-section">
      {error && <div className="banner error">⚠ {error}</div>}
      <div className="lib-head">
        <h2>Connectors</h2>
        <p>External issue trackers, defined once for the instance. Bind a connector to a project (and set its project-side scope) from that project's Integrations surface.</p>
      </div>

      <div className="conn-list">
        {connectors.length === 0 && <div className="orch-empty">No connectors yet — add one below.</div>}
        {connectors.map((c) => (
          <div key={c.id} className="conn-row">
            <span className="dot on" />
            <span className="conn-id">
              <span className="conn-name">{c.name} <span className="type-badge">{c.type}{pullable(c.type) ? ' · push+pull' : ' · push'}</span></span>
              <span className="conn-meta">{c.base_url}{c.credential ? ` · 🔑 ${c.credential}` : ''}</span>
            </span>
            <button className="del" title="Delete" onClick={() => del(c.id)}>✕</button>
          </div>
        ))}
      </div>

      <div className="prov-add">
        <div className="prov-add-title">Add connector</div>
        <input placeholder="name (e.g. DefectDojo prod)" value={name} onChange={(e) => setName(e.target.value)} />
        <select value={type} onChange={(e) => setType(e.target.value)}>
          {types.map((t) => <option key={t.type} value={t.type}>{t.type}</option>)}
        </select>
        <input placeholder="base URL (e.g. https://dd.example.com)" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
        <select value={credential} onChange={(e) => setCredential(e.target.value)}>
          <option value="">credential (vault secret) — optional</option>
          {secrets.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <button className="prov-add-btn" onClick={add} disabled={!online || !name.trim() || !baseUrl.trim()}>＋ Add connector</button>
        <div className="prov-hint">The credential is a vault secret name, never a value. Bind this connector to projects from their Integrations surface.</div>
      </div>
    </div>
  )
}
