import { useEffect, useState, type FormEvent } from 'react'
import { api, Project, ScopeEntry } from './api'
import { EngagementSettings } from './EngagementSettings'
import { IntegrationsTab } from './IntegrationsTab'
import { SecretsLibrary } from './SecretsLibrary'

// The per-project config sections, consolidated behind one Settings surface (was four separate
// activity-bar surfaces: Settings/Scope/Integrations/Secrets). A left sub-nav mirrors the global
// Settings page; the folded surfaces move here as sections so the activity bar carries only the work.
export type SettingsSection = 'engagement' | 'scope' | 'integrations' | 'secrets'

const SECTION_GROUPS: { group: string; items: { id: SettingsSection; icon: string; label: string }[] }[] = [
  { group: 'Project', items: [
    { id: 'engagement', icon: '📋', label: 'Engagement' },
    { id: 'scope', icon: '🛡', label: 'Scope' },
  ] },
  { group: 'Connections', items: [
    { id: 'integrations', icon: '🔌', label: 'Integrations' },
    { id: 'secrets', icon: '🔒', label: 'Secrets' },
  ] },
]

export function ProjectSettings({
  project,
  online,
  onError,
  onSaved,
  initialSection,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
  onSaved: () => void
  initialSection?: SettingsSection
}) {
  const [active, setActive] = useState<SettingsSection>(initialSection ?? 'engagement')
  // A deep-link (Overview jump, agent "show") can retarget the section while the surface is already open.
  useEffect(() => {
    if (initialSection) setActive(initialSection)
  }, [initialSection])

  return (
    <div className="psettings">
      <nav className="psettings-nav">
        {SECTION_GROUPS.map((g) => (
          <div key={g.group} className="psettings-grp">
            <div className="psettings-grp-h">{g.group}</div>
            {g.items.map((t) => (
              <button key={t.id} className={active === t.id ? 'on' : ''} onClick={() => setActive(t.id)}>
                <span className="ico">{t.icon}</span> {t.label}
              </button>
            ))}
          </div>
        ))}
      </nav>
      <div className="psettings-body">
        {active === 'engagement' && <EngagementSettings project={project} online={online} onError={onError} onSaved={onSaved} />}
        {active === 'scope' && <ScopeTab project={project} online={online} onError={onError} />}
        {active === 'integrations' && <IntegrationsTab project={project} online={online} onError={onError} />}
        {active === 'secrets' && <SecretsLibrary online={online} projectId={project.id} />}
      </div>
    </div>
  )
}

// ScopeTab is the in-scope allowlist network capabilities honor (was its own activity-bar surface).
function ScopeTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [entries, setEntries] = useState<ScopeEntry[]>([])
  const [kind, setKind] = useState('host')
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)

  async function reload() {
    setEntries((await api.listScope(project.id)) ?? [])
  }

  useEffect(() => {
    if (online) void reload().catch((e) => onError((e as Error).message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  async function add(e: FormEvent) {
    e.preventDefault()
    if (!value.trim()) return
    setBusy(true)
    try {
      await api.addScope(project.id, kind, value.trim())
      setValue('')
      await reload()
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function remove(id: string) {
    try {
      await api.deleteScope(id)
      await reload()
    } catch (err) {
      onError((err as Error).message)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">In-scope allowlist</div>
      <p className="hint">
        Network capabilities (e.g. HTTP probe) may only touch targets that match an entry below. An
        empty allowlist imposes no restriction.
      </p>
      <form className="create-row" onSubmit={add}>
        <select value={kind} onChange={(e) => setKind(e.target.value)}>
          {['host', 'domain', 'cidr'].map((k) => (
            <option key={k} value={k}>{k}</option>
          ))}
        </select>
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={kind === 'cidr' ? '10.0.0.0/24' : kind === 'domain' ? 'acme.com' : 'api.acme.com'}
          disabled={!online || busy}
        />
        <button type="submit" disabled={!online || busy || !value.trim()}>
          {busy ? 'Adding…' : '＋ Add'}
        </button>
      </form>
      {entries.length === 0 ? (
        <div className="empty">No scope entries — all targets are allowed.</div>
      ) : (
        <ul className="rows">
          {entries.map((e) => (
            <li key={e.id} className="row-item">
              <span className="badge">{e.kind}</span>
              <span className="row-title">{e.value}</span>
              <button className="link danger" onClick={() => remove(e.id)}>remove</button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
