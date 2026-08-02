import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { api, SettingSection } from './api'
import { applyTheme } from './theme'
import { Providers } from './Providers'
import { Tags } from './Tags'
import { ApprovalPolicy } from './ApprovalPolicy'
import { GovernanceProfile } from './GovernanceProfile'
import { ConnectorsLibrary } from './ConnectorsLibrary'

// Custom (bespoke-component) sections, composed alongside declarative ones from the API (ADR-0021).
// Custom Agents moved to the global Library (build & reuse) — see Library.tsx. Governance (LLM egress)
// and Connectors moved here from the retired "Extensions & Governance" rail / Library.
const CUSTOM: { id: string; title: string; icon: string; order: number }[] = [
  { id: 'providers', title: 'Models & Providers', icon: '🧠', order: 12 },
  { id: 'approvals', title: 'Approvals', icon: '🛡', order: 30 },
  { id: 'governance', title: 'Governance', icon: '⧉', order: 35 },
  { id: 'connectors', title: 'Connectors', icon: '🔌', order: 45 },
]

// Sections are bucketed into three categories in the nav (operational vs general settings vs policy),
// rendered in this order. Grouping is a frontend concern — the backend Section has no category field —
// so we map by section id; extension-contributed sections (source "ext:…") fall through to Extensions.
// Within a group, sections keep their `order`. IDs not listed here default to General.
const GROUP_ORDER = ['General', 'Operational', 'Policy', 'Extensions'] as const
const GROUP_BY_ID: Record<string, (typeof GROUP_ORDER)[number]> = {
  appearance: 'General',
  notifications: 'General',
  providers: 'Operational',
  engagements: 'Operational',
  runtime: 'Operational',
  connectors: 'Operational',
  approvals: 'Policy',
  governance: 'Policy',
}
function groupOf(id: string, source?: string): (typeof GROUP_ORDER)[number] {
  return GROUP_BY_ID[id] ?? (source?.startsWith('ext:') ? 'Extensions' : 'General')
}

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

  // Declarative sections from the API + custom sections, ordered, then bucketed into nav groups.
  const declarative = sections.filter((s) => !s.custom)
  const tabs = [
    ...declarative.map((s) => ({ id: s.id, title: s.title, icon: s.icon ?? '•', order: s.order, source: s.source })),
    ...CUSTOM.map((c) => ({ ...c, source: undefined as string | undefined })),
  ].sort((a, b) => a.order - b.order)
  const navGroups = GROUP_ORDER.map((g) => ({ label: g, items: tabs.filter((t) => groupOf(t.id, t.source) === g) })).filter(
    (g) => g.items.length > 0,
  )

  async function setValue(key: string, value: string) {
    const next = { ...values, [key]: value }
    setValues(next)
    if (key.startsWith('appearance.'))
      applyTheme(next['appearance.theme'] || 'dark', next['appearance.accent'] || '', next['appearance.text_size'] || '1')
    try {
      await api.putSettings({ [key]: value })
    } catch {
      /* keep the optimistic value; a reload would correct it */
    }
  }

  const activeSection = declarative.find((s) => s.id === active)

  // Tabs share one scroll container (.settings-body) that stays mounted across switches; reset to the top
  // on switch so a shorter section can't inherit a taller one's scrollTop and end up stranded past its end.
  const bodyRef = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    if (bodyRef.current) bodyRef.current.scrollTop = 0
  }, [active])

  return (
    <div className="settings">
      <nav className="settings-nav">
        {navGroups.map((g) => (
          <div key={g.label} className="settings-navgrp">
            <div className="settings-navgrp-h">{g.label}</div>
            {g.items.map((t) => (
              <button key={t.id} className={active === t.id ? 'on' : ''} onClick={() => setActive(t.id)}>
                <span className="settings-ico">{t.icon}</span> {t.title}
              </button>
            ))}
          </div>
        ))}
      </nav>
      <div className="settings-body" ref={bodyRef}>
        {active === 'providers' && (
          <>
            <Providers online={online} />
            <Tags online={online} />
          </>
        )}
        {active === 'approvals' && <ApprovalPolicy online={online} />}
        {active === 'governance' && <GovernanceProfile online={online} />}
        {active === 'connectors' && <ConnectorsLibrary online={online} />}
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
