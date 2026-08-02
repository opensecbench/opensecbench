import { useEffect, useRef, useState } from 'react'
import { api, Secret } from './api'

// SecretsLibrary manages the encrypted vault (ADR-0011): values are sealed with AES-256-GCM and never
// leave the backend, so this surface shows only *which* secrets are set (name + timestamps) and lets the
// user add, replace, or delete them. With no projectId it manages the app-wide (global) vault; with a
// projectId it manages that project's own vault (ADR-0049) and additionally lists the inherited global
// secrets read-only. A project secret shadows a global one of the same name at run time.
export function SecretsLibrary({ online, projectId }: { online: boolean; projectId?: string }) {
  const [secrets, setSecrets] = useState<Secret[]>([])
  const [inherited, setInherited] = useState<Secret[]>([]) // global secrets, shown read-only in project scope
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const valueRef = useRef<HTMLInputElement>(null)

  async function load() {
    try {
      setSecrets((projectId ? await api.listProjectSecrets(projectId) : await api.listSecrets()) ?? [])
      // In project scope, also fetch the app-wide secrets to show what this project inherits.
      setInherited(projectId ? ((await api.listSecrets()) ?? []) : [])
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    if (!online) return
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, projectId])

  const own = new Set(secrets.map((s) => s.name))
  const existing = own.has(name.trim())
  // Inherited globals that this project has NOT overridden — the ones actually in effect from the app scope.
  const inheritedActive = inherited.filter((s) => !own.has(s.name))

  async function save() {
    const n = name.trim()
    if (!n || !value) return
    setBusy(true)
    try {
      if (projectId) await api.setProjectSecret(projectId, n, value)
      else await api.setSecret(n, value)
      setName(''); setValue('')
      setError(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  async function del(n: string) {
    if (!window.confirm(`Delete secret "${n}"? Anything referencing it (connectors, tasks, integrations) will fail to resolve.`)) return
    try {
      if (projectId) await api.deleteProjectSecret(projectId, n)
      else await api.deleteSecret(n)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }
  // Values are write-only — pre-fill the name and focus the value field so a replacement can be typed.
  function replace(n: string) {
    setName(n)
    setValue('')
    valueRef.current?.focus()
  }

  return (
    <div className="lib-section">
      {error && <div className="banner error">⚠ {error}</div>}
      <div className="lib-head">
        <h2>{projectId ? 'Project secrets' : 'Secrets'}</h2>
        <p>
          Credentials sealed in the encrypted vault (AES-256-GCM). Values never leave the backend — this surface shows only which secrets are set.
          {projectId
            ? ' These belong to this project and are sealed with its own key; a project secret with the same name as a global one takes precedence at run time.'
            : ' Reference a secret by name from connectors, integrations, and scanner tasks.'}
        </p>
      </div>

      <div className="conn-list">
        {secrets.length === 0 && <div className="orch-empty">No {projectId ? 'project ' : ''}secrets yet — add one below.</div>}
        {secrets.map((s) => (
          <div key={s.id} className="conn-row">
            <span className="dot on" />
            <span className="conn-id">
              <span className="conn-name">
                🔒 {s.name} <span className="type-badge">set</span>
                {projectId && inherited.some((g) => g.name === s.name) && <span className="type-badge"> overrides global</span>}
              </span>
              <span className="conn-meta">updated {new Date(s.updated_at).toLocaleDateString()}</span>
            </span>
            <button className="del" title="Replace value" onClick={() => replace(s.name)}>↻</button>
            <button className="del" title="Delete" onClick={() => del(s.name)}>✕</button>
          </div>
        ))}
      </div>

      {projectId && inheritedActive.length > 0 && (
        <div className="conn-list">
          <div className="prov-add-title" style={{ marginBottom: 6 }}>Inherited from app (read-only)</div>
          {inheritedActive.map((s) => (
            <div key={s.id} className="conn-row" style={{ opacity: 0.7 }}>
              <span className="dot on" />
              <span className="conn-id">
                <span className="conn-name">🌐 {s.name} <span className="type-badge">inherited</span></span>
                <span className="conn-meta">from app scope · add a project secret of the same name to override</span>
              </span>
              <button className="del" title="Override in this project" onClick={() => replace(s.name)}>↻</button>
            </div>
          ))}
        </div>
      )}

      <div className="prov-add">
        <div className="prov-add-title">{existing ? 'Replace secret value' : projectId ? 'Add project secret' : 'Add secret'}</div>
        <input placeholder="name (e.g. defectdojo_api_key)" value={name} onChange={(e) => setName(e.target.value)} />
        <input ref={valueRef} type="password" autoComplete="new-password" placeholder="value (sealed on save, never shown again)" value={value} onChange={(e) => setValue(e.target.value)} />
        <button className="prov-add-btn" onClick={save} disabled={!online || busy || !name.trim() || !value}>
          {busy ? 'Sealing…' : existing ? '↻ Replace value' : '＋ Add secret'}
        </button>
        <div className="prov-hint">Setting an existing name replaces its value. The plaintext is sealed immediately and is never persisted in the clear, logged, or echoed back.</div>
      </div>
    </div>
  )
}
