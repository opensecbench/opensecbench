import { useEffect, useRef, useState } from 'react'
import { api, Secret } from './api'

// SecretsLibrary manages the encrypted vault (ADR-0011): the values are sealed with AES-256-GCM and never
// leave the backend, so this surface shows only *which* secrets are set (name + timestamps) and lets the
// user add, replace, or delete them. Secret values are referenced by name from connectors, integrations,
// scanner tasks, and engagement test accounts.
export function SecretsLibrary({ online }: { online: boolean }) {
  const [secrets, setSecrets] = useState<Secret[]>([])
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const valueRef = useRef<HTMLInputElement>(null)

  async function load() {
    try {
      setSecrets((await api.listSecrets()) ?? [])
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    if (!online) return
    void load()
  }, [online])

  const existing = secrets.some((s) => s.name === name.trim())

  async function save() {
    const n = name.trim()
    if (!n || !value) return
    setBusy(true)
    try {
      await api.setSecret(n, value)
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
      await api.deleteSecret(n)
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
        <h2>Secrets</h2>
        <p>Credentials sealed in the encrypted vault (AES-256-GCM). Values never leave the backend — this surface shows only which secrets are set. Reference a secret by name from connectors, integrations, and scanner tasks.</p>
      </div>

      <div className="conn-list">
        {secrets.length === 0 && <div className="orch-empty">No secrets yet — add one below.</div>}
        {secrets.map((s) => (
          <div key={s.id} className="conn-row">
            <span className="dot on" />
            <span className="conn-id">
              <span className="conn-name">🔒 {s.name} <span className="type-badge">set</span></span>
              <span className="conn-meta">updated {new Date(s.updated_at).toLocaleDateString()}</span>
            </span>
            <button className="del" title="Replace value" onClick={() => replace(s.name)}>↻</button>
            <button className="del" title="Delete" onClick={() => del(s.name)}>✕</button>
          </div>
        ))}
      </div>

      <div className="prov-add">
        <div className="prov-add-title">{existing ? 'Replace secret value' : 'Add secret'}</div>
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
