import { useEffect, useState } from 'react'
import { api, Engagement, Project } from './api'
import { hasNativePickers, pickDirectory } from './native'
import { TECHNIQUES } from './EngagementModal'

// Project Settings surface (ADR-0051): edit an engagement's record after creation — the modal is create-only,
// but scope/data-class/rules-of-engagement/authorization must be revisable mid-engagement since they drive
// enforcement. Bound to GET/PUT /v1/projects/{id}/engagement. (Scope entries have their own Scope tab.)
export function EngagementSettings({
  project,
  online,
  onError,
  onSaved,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
  onSaved: () => void
}) {
  const [eng, setEng] = useState<Engagement | null>(null)
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (!online) return
    api.getEngagement(project.id).then((e) => setEng(e)).catch((err) => onError((err as Error).message))
  }, [online, project.id, onError])

  if (!eng) return <div className="content"><div className="empty">{online ? 'Loading engagement…' : 'Offline.'}</div></div>

  const patch = (p: Partial<Engagement>) => { setEng({ ...eng, ...p }); setSaved(false) }
  const techniques = eng.techniques ?? {}
  const contacts = eng.contacts ?? []
  const accounts = eng.test_accounts ?? []

  async function save() {
    if (!eng) return
    setBusy(true)
    try {
      const clean: Engagement = {
        ...eng,
        contacts: contacts.filter((c) => c.name || c.email),
        test_accounts: accounts.filter((a) => a.username || a.role),
      }
      const updated = await api.setEngagement(project.id, clean)
      setEng(updated)
      setSaved(true)
      onSaved()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="content">
      <div className="hero"><h1>Engagement settings</h1><p>The frame this project is governed by — scope posture, rules of engagement, authorization, and reporting.</p></div>

      <section className="panel es">
        <div className="panel-head">Identity</div>
        <div className="em-field">
          <label>Objective &amp; success criteria</label>
          <textarea className="em-in" rows={2} value={eng.objective ?? ''} onChange={(e) => patch({ objective: e.target.value })} />
        </div>
        <div className="em-field"><label>Reference <span className="em-opt">SOW / ticket</span></label>
          <input className="em-in" value={eng.reference ?? ''} onChange={(e) => patch({ reference: e.target.value })} /></div>
        <div className="em-field">
          <label>Base folder <span className="em-opt">relative asset paths resolve against it</span></label>
          <div className="em-browse">
            <input className="em-in" value={eng.base_path ?? ''} onChange={(e) => patch({ base_path: e.target.value })} placeholder="/home/you/src/acme" />
            {hasNativePickers() && <button className="em-btn" onClick={async () => { const p = await pickDirectory((eng.base_path ?? '') || undefined); if (p) patch({ base_path: p }) }}>📁 Browse…</button>}
          </div>
        </div>
        <div className="em-two">
          <div className="em-field"><label>Program URL <span className="em-opt">bug bounty program page</span></label>
            <input className="em-in" value={eng.program_url ?? ''} onChange={(e) => patch({ program_url: e.target.value })} placeholder="https://hackerone.com/acme" /></div>
          <div className="em-field"><label>Platform</label>
            <select className="em-in" value={eng.platform ?? ''} onChange={(e) => patch({ platform: e.target.value })}>
              <option value="">— None —</option>
              <option value="hackerone">HackerOne</option>
              <option value="bugcrowd">Bugcrowd</option>
              <option value="intigriti">Intigriti</option>
              <option value="independent">Independent</option>
            </select></div>
        </div>
      </section>

      <section className="panel es">
        <div className="panel-head">Scope &amp; authorization <span className="es-note">enforced</span></div>
        <div className="em-two">
          <div className="em-field"><label>Environment</label>
            <div className="em-seg">{['production', 'staging', 'dev'].map((e) => (
              <button key={e} className={eng.environment === e ? 'on' : ''} onClick={() => patch({ environment: e })}>{e === 'production' ? 'Prod' : e === 'staging' ? 'Staging' : 'Dev'}</button>
            ))}</div></div>
          <div className="em-field"><label>Data sensitivity <span className="em-opt">gates external AI</span></label>
            <div className="em-seg">{['open', 'private', 'restricted'].map((d) => (
              <button key={d} className={eng.data_class === d ? 'on' : ''} onClick={() => patch({ data_class: d })}>{d[0].toUpperCase() + d.slice(1)}</button>
            ))}</div></div>
        </div>
        <div className="em-field">
          <label className="em-check" onClick={() => patch({ authorized: !eng.authorized })}>
            <span className={`em-box ${eng.authorized ? 'on' : ''}`}>{eng.authorized ? '✓' : ''}</span>
            Written authorization is on file for this work
          </label>
          <div className="em-two" style={{ marginTop: 8 }}>
            <input className="em-in" value={eng.authorizer ?? ''} onChange={(e) => patch({ authorizer: e.target.value })} placeholder="Authorizer (name / email)" />
            <label className="em-inline">valid until <input className="em-in" type="date" value={eng.auth_to ?? ''} onChange={(e) => patch({ auth_to: e.target.value })} /></label>
          </div>
        </div>
        <div className="em-field">
          <label>Allowed techniques <span className="em-opt">disallowed ones are blocked</span></label>
          <div className="em-toggles">{TECHNIQUES.map((t) => (
            <button key={t.k} className="em-tog" onClick={() => patch({ techniques: { ...techniques, [t.k]: !techniques[t.k] } })}>
              <span className={`em-sw ${techniques[t.k] ? 'on' : ''}`}><i /></span> {t.label}
            </button>
          ))}</div>
        </div>
      </section>

      <section className="panel es">
        <div className="panel-head">Timeline &amp; reporting</div>
        <div className="em-three">
          <label className="em-inline">Start <input className="em-in" type="date" value={eng.window_start ?? ''} onChange={(e) => patch({ window_start: e.target.value })} /></label>
          <label className="em-inline">End <input className="em-in" type="date" value={eng.window_end ?? ''} onChange={(e) => patch({ window_end: e.target.value })} /></label>
          <label className="em-inline">Report due <input className="em-in" type="date" value={eng.report_due ?? ''} onChange={(e) => patch({ report_due: e.target.value })} /></label>
        </div>
        <div className="em-three" style={{ marginTop: 10 }}>
          <input className="em-in" value={eng.standard ?? ''} onChange={(e) => patch({ standard: e.target.value })} placeholder="Standard (OWASP WSTG, ASVS L2…)" />
          <input className="em-in" value={eng.compliance ?? ''} onChange={(e) => patch({ compliance: e.target.value })} placeholder="Compliance (PCI, SOC2…)" />
          <select className="em-in" value={eng.severity_scale ?? 'cvss31'} onChange={(e) => patch({ severity_scale: e.target.value })}>
            <option value="cvss31">CVSS 3.1</option><option value="cvss40">CVSS 4.0</option><option value="custom">Custom</option>
          </select>
        </div>
      </section>

      <section className="panel es">
        <div className="panel-head">Contacts <button className="em-add" onClick={() => patch({ contacts: [...contacts, { role: 'technical', name: '' }] })}>+ add</button></div>
        {contacts.length === 0 && <div className="em-hint">No contacts recorded.</div>}
        {contacts.map((c, i) => (
          <div key={i} className="em-three" style={{ marginBottom: 6 }}>
            <select className="em-in" value={c.role} onChange={(e) => patch({ contacts: patchAt(contacts, i, { role: e.target.value }) })}>
              <option value="technical">Technical POC</option><option value="authorizer">Authorizer</option><option value="breakglass">Break-glass</option>
            </select>
            <input className="em-in" value={c.name} onChange={(e) => patch({ contacts: patchAt(contacts, i, { name: e.target.value }) })} placeholder="Name" />
            <input className="em-in" value={c.email ?? ''} onChange={(e) => patch({ contacts: patchAt(contacts, i, { email: e.target.value }) })} placeholder="Email / phone" />
          </div>
        ))}
        <div className="panel-head" style={{ marginTop: 14 }}>Test accounts <button className="em-add" onClick={() => patch({ test_accounts: [...accounts, { role: 'user', username: '' }] })}>+ add</button></div>
        {accounts.length === 0 && <div className="em-hint">No test accounts recorded. Store only a vault secret reference here.</div>}
        {accounts.map((a, i) => (
          <div key={i} className="em-three" style={{ marginBottom: 6 }}>
            <input className="em-in" value={a.role} onChange={(e) => patch({ test_accounts: patchAt(accounts, i, { role: e.target.value }) })} placeholder="Role (admin/user/anon)" />
            <input className="em-in" value={a.username ?? ''} onChange={(e) => patch({ test_accounts: patchAt(accounts, i, { username: e.target.value }) })} placeholder="Username" />
            <input className="em-in" value={a.secret_ref ?? ''} onChange={(e) => patch({ test_accounts: patchAt(accounts, i, { secret_ref: e.target.value }) })} placeholder="Vault secret ref" />
          </div>
        ))}
      </section>

      <div className="es-actions">
        {saved && <span className="es-saved">✓ Saved</span>}
        <button className="pbuild-save" disabled={!online || busy} onClick={save}>{busy ? 'Saving…' : 'Save changes'}</button>
      </div>
    </div>
  )
}

function patchAt<T>(arr: T[], i: number, p: Partial<T>): T[] {
  return arr.map((x, j) => (j === i ? { ...x, ...p } : x))
}
