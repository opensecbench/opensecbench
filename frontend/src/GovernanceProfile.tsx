import { useEffect, useState } from 'react'
import { api, PolicyProfile } from './api'

// GovernanceProfile is the LLM-egress posture control (moved from the retired "Extensions & Governance"
// rail into Settings): it picks which policy profile governs whether the Analyst may send private-asset
// content to an external LLM provider. A global setting, so it sits with the other Settings policy panes.
export function GovernanceProfile({ online }: { online: boolean }) {
  const [profiles, setProfiles] = useState<PolicyProfile[]>([])
  const [active, setActive] = useState<string>('')
  const [msg, setMsg] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    if (!online) return
    void (async () => {
      try {
        setProfiles((await api.listPolicyProfiles()) ?? [])
        setActive((await api.getActivePolicy()).name)
      } catch (e) {
        setErr((e as Error).message)
      }
    })()
  }, [online])

  async function switchPolicy(name: string) {
    try {
      setActive((await api.setActivePolicy(name)).name)
      setMsg(`Governance profile set to ${name}.`)
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  return (
    <div className="lib-section">
      {err && <div className="banner error">⚠ {err}</div>}
      {msg && <div className="banner">{msg}</div>}
      <div className="lib-head">
        <h2>Governance</h2>
        <p>Controls whether the Analyst may send private-asset content to an external LLM provider.</p>
      </div>

      <section className="panel">
        <div className="panel-head">Governance profile</div>
        <div className="rows">
          {profiles.map((p) => (
            <label key={p.name} className={`row-item clickable ${active === p.name ? 'on' : ''}`}>
              <input type="radio" name="policy" checked={active === p.name} onChange={() => switchPolicy(p.name)} disabled={!online} />
              <span className="row-title">{p.name}</span>
              <span className="muted">{p.description}</span>
            </label>
          ))}
        </div>
      </section>
    </div>
  )
}
