import { useEffect, useState } from 'react'
import { api, ExtensionInfo, HubPackage } from './api'

// Extensions is a global Library ▸ Configure section (IA declutter): installed third-party packages and
// the community hub to browse and trust-install new ones. LLM-egress governance, which used to sit
// alongside these, is now per-connection (Settings ▸ Models & Providers ▸ Data clearance).
export function Extensions({ online }: { online: boolean }) {
  const [installed, setInstalled] = useState<ExtensionInfo[]>([])
  const [hubURL, setHubURL] = useState('')
  const [pkgs, setPkgs] = useState<HubPackage[]>([])
  const [msg, setMsg] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)

  async function reloadInstalled() {
    setInstalled((await api.listExtensions()) ?? [])
  }

  useEffect(() => {
    if (!online) return
    void (async () => {
      try {
        await reloadInstalled()
      } catch (e) {
        setErr((e as Error).message)
      }
    })()
  }, [online])

  async function browse() {
    setErr(null)
    setMsg(null)
    try {
      const res = await api.hubIndex(hubURL)
      setPkgs(res.packages ?? [])
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  async function install(p: HubPackage) {
    setErr(null)
    try {
      const info = await api.hubInstall(hubURL, p.id, true, false)
      setMsg(`Installed ${info.id} v${info.version} (trusted=${info.trusted}).`)
      await reloadInstalled()
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  return (
    <div className="lib-section">
      {err && <div className="banner error">⚠ {err}</div>}
      {msg && <div className="banner">{msg}</div>}
      <div className="lib-head">
        <h2>Extensions</h2>
        <p>Third-party packages installed for the instance, and the community hub to browse and trust-install more.</p>
      </div>

      <section className="panel">
        <div className="panel-head">Installed extensions</div>
        {installed.length === 0 ? (
          <div className="empty">No extensions installed.</div>
        ) : (
          <ul className="rows">
            {installed.map((e) => (
              <li key={e.id} className="row-item">
                <span className={`badge ${e.trusted ? 'succeeded' : 'failed'}`}>{e.trusted ? 'trusted' : 'untrusted'}</span>
                <span className="row-title">{e.name || e.id} <span className="muted">v{e.version}</span></span>
                <span className="muted mono">{(e.capabilities ?? []).join(', ')}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="panel">
        <div className="panel-head">Community hub</div>
        <p className="hint">Browse and install signed packages. Installing trusts the publisher's key (explicit consent).</p>
        <div className="create-row">
          <input value={hubURL} onChange={(e) => setHubURL(e.target.value)} placeholder="https://hub.example/osb" disabled={!online} />
          <button onClick={browse} disabled={!online || !hubURL.trim()}>Browse</button>
        </div>
        {pkgs.length > 0 && (
          <ul className="rows">
            {pkgs.map((p) => (
              <li key={p.id} className="row-item">
                <span className="row-title">{p.name || p.id} <span className="muted">v{p.version} · {p.publisher}</span></span>
                <span className="muted">{p.description}</span>
                <button className="link" onClick={() => install(p)}>trust & install</button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}
