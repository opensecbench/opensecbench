import { useEffect, useState } from 'react'
import { api, SettingSection } from './api'
import { applyTheme } from './theme'
import { Providers } from './Providers'
import { ModelRouting } from './ModelRouting'
import { ApprovalPolicy } from './ApprovalPolicy'
import { CustomAgents } from './CustomAgents'

// Custom (bespoke-component) sections, composed alongside declarative ones from the API (ADR-0021).
const CUSTOM: { id: string; title: string; icon: string; order: number }[] = [
  { id: 'providers', title: 'Models & Providers', icon: '🧠', order: 20 },
  { id: 'routing', title: 'Model Routing', icon: '🎯', order: 25 },
  { id: 'approvals', title: 'Approvals', icon: '🛡', order: 30 },
  { id: 'agents', title: 'Custom Agents', icon: '🤖', order: 40 },
]

export function Settings({ online }: { online: boolean }) {
  const [sections, setSections] = useState<SettingSection[]>([])
  const [values, setValues] = useState<Record<string, string>>({})
  const [active, setActive] = useState('appearance')

  useEffect(() => {
    if (!online) return
    void api
      .getSettings()
      .then((s) => {
        setSections(s.sections ?? [])
        setValues(s.values ?? {})
      })
      .catch(() => {})
  }, [online])

  // Declarative sections from the API + custom sections, ordered.
  const declarative = sections.filter((s) => !s.custom)
  const tabs = [...declarative.map((s) => ({ id: s.id, title: s.title, icon: s.icon ?? '•', order: s.order })), ...CUSTOM].sort(
    (a, b) => a.order - b.order,
  )

  async function setValue(key: string, value: string) {
    const next = { ...values, [key]: value }
    setValues(next)
    if (key.startsWith('appearance.')) applyTheme(next['appearance.theme'] || 'dark', next['appearance.accent'] || '')
    try {
      await api.putSettings({ [key]: value })
    } catch {
      /* keep the optimistic value; a reload would correct it */
    }
  }

  const activeSection = declarative.find((s) => s.id === active)

  return (
    <div className="settings">
      <nav className="settings-nav">
        {tabs.map((t) => (
          <button key={t.id} className={active === t.id ? 'on' : ''} onClick={() => setActive(t.id)}>
            <span className="settings-ico">{t.icon}</span> {t.title}
          </button>
        ))}
      </nav>
      <div className="settings-body">
        {active === 'providers' && <Providers online={online} />}
        {active === 'routing' && <ModelRouting online={online} />}
        {active === 'approvals' && <ApprovalPolicy online={online} />}
        {active === 'agents' && <CustomAgents online={online} />}
        {activeSection && (
          <div className="settings-fields">
            <h2>
              {activeSection.title}
              {activeSection.source?.startsWith('ext:') && (
                <span className="settings-ext" title={`Provided by extension ${activeSection.source.slice(4)}`}>extension</span>
              )}
            </h2>
            {(activeSection.fields ?? []).map((f) => (
              <div key={f.key} className="settings-field">
                <label>{f.label}</label>
                <Field type={f.type} value={values[f.key] ?? f.default ?? ''} options={f.options} onChange={(v) => setValue(f.key, v)} />
                {f.description && <div className="settings-desc">{f.description}</div>}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

// Field renders one typed declarative setting (ADR-0021 §2) — the generic renderer core + extension
// sections both flow through.
function Field({
  type,
  value,
  options,
  onChange,
}: {
  type: string
  value: string
  options?: { value: string; label: string }[]
  onChange: (v: string) => void
}) {
  switch (type) {
    case 'select':
      return (
        <select value={value} onChange={(e) => onChange(e.target.value)}>
          {(options ?? []).map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
      )
    case 'color':
      return (
        <span className="settings-color">
          <input type="color" value={value || '#000000'} onChange={(e) => onChange(e.target.value)} />
          <input className="settings-color-hex" value={value} onChange={(e) => onChange(e.target.value)} />
        </span>
      )
    case 'bool':
      return <input type="checkbox" checked={value === 'true'} onChange={(e) => onChange(e.target.checked ? 'true' : 'false')} />
    case 'number':
      return <input type="number" value={value} onChange={(e) => onChange(e.target.value)} />
    case 'text':
      return <textarea value={value} onChange={(e) => onChange(e.target.value)} />
    default:
      return <input value={value} onChange={(e) => onChange(e.target.value)} />
  }
}
