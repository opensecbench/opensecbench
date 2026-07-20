import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { api, Engagement, EngagementContact, EngagementTestAccount, Methodology, Project, ScopeSeed, Template } from './api'

// The engagement setup modal (ADR-0051): create a project with its properties in one place instead of a bare
// name field. Captures the frame of an assessment — identity, scope + authorization, rules of engagement,
// kickstart, and (collapsed) the long tail — then creates the project with its engagement record + scope in
// one call, and best-effort seeds methodology adoption + a first asset.

const KINDS = [
  { k: 'web', label: 'Web app' }, { k: 'api', label: 'REST API' }, { k: 'graphql', label: 'GraphQL' },
  { k: 'mobile', label: 'Mobile' }, { k: 'cloud', label: 'Cloud' }, { k: 'network', label: 'Network / infra' },
  { k: 'code', label: 'Code / SAST' }, { k: 'secrets', label: 'Secrets audit' }, { k: 'threat-model', label: 'Threat model' },
  { k: 'red-team', label: 'Red team' },
]
const TECHNIQUES = [
  { k: 'intrusive', label: 'Intrusive scanning' }, { k: 'automated_exploit', label: 'Automated exploitation' },
  { k: 'brute_force', label: 'Brute force / cred stuffing' }, { k: 'dos', label: 'DoS / stress' },
  { k: 'social', label: 'Social engineering' }, { k: 'destructive', label: 'Destructive / data-altering' },
]
// A template preset just pre-fills the form — it is not a separate create path (ADR-0051).
const TEMPLATE_KINDS: Record<string, string[]> = {
  'web-app': ['web'], 'rest-api': ['api'], graphql: ['graphql'], mobile: ['mobile'], 'cloud-aws': ['cloud'],
}

function inferKind(value: string): string {
  if (value.includes('/')) return 'cidr'
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) return 'host'
  return 'domain'
}

// A chips input: type a value, Enter adds it; each token is removable.
function ScopeInput({ tokens, onChange, deny }: { tokens: string[]; onChange: (t: string[]) => void; deny?: boolean }) {
  const [v, setV] = useState('')
  function add() {
    const t = v.trim()
    if (t && !tokens.includes(t)) onChange([...tokens, t])
    setV('')
  }
  function key(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add() }
    else if (e.key === 'Backspace' && !v && tokens.length) onChange(tokens.slice(0, -1))
  }
  return (
    <div className={`em-chips ${deny ? 'deny' : ''}`}>
      {tokens.map((t) => (
        <span key={t} className={`em-tok ${deny ? 'no' : ''}`}>{t}<button onClick={() => onChange(tokens.filter((x) => x !== t))}>×</button></span>
      ))}
      <input value={v} onChange={(e) => setV(e.target.value)} onKeyDown={key} onBlur={add}
        placeholder={tokens.length ? '' : deny ? 'exclude a host / domain / CIDR…' : 'host, domain, or CIDR…'} />
    </div>
  )
}

export function EngagementModal({
  online,
  templates,
  onClose,
  onCreated,
}: {
  online: boolean
  templates: Template[]
  onClose: () => void
  onCreated: (p: Project) => void
}) {
  const [name, setName] = useState('')
  const [kinds, setKinds] = useState<string[]>([])
  const [objective, setObjective] = useState('')
  const [reference, setReference] = useState('')
  const [inScope, setInScope] = useState<string[]>([])
  const [outScope, setOutScope] = useState<string[]>([])
  const [environment, setEnvironment] = useState('staging')
  const [dataClass, setDataClass] = useState('private')
  const [authorized, setAuthorized] = useState(false)
  const [authorizer, setAuthorizer] = useState('')
  const [authTo, setAuthTo] = useState('')
  const [techniques, setTechniques] = useState<Record<string, boolean>>({ intrusive: true })
  // kickstart
  const [template, setTemplate] = useState('')
  const [firstRepo, setFirstRepo] = useState('')
  const [adopt, setAdopt] = useState<string[]>([])
  const [tracker, setTracker] = useState('')
  // advanced
  const [showAdv, setShowAdv] = useState(false)
  const [windowStart, setWindowStart] = useState('')
  const [windowEnd, setWindowEnd] = useState('')
  const [reportDue, setReportDue] = useState('')
  const [standard, setStandard] = useState('')
  const [compliance, setCompliance] = useState('')
  const [severity, setSeverity] = useState('cvss31')
  const [contacts, setContacts] = useState<EngagementContact[]>([])
  const [testAccounts, setTestAccounts] = useState<EngagementTestAccount[]>([])

  const [methodologies, setMethodologies] = useState<Methodology[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const nameRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    nameRef.current?.focus()
    if (online) api.listMethodologies().then((m) => setMethodologies(m ?? [])).catch(() => {})
    const onKey = (e: globalThis.KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  function applyTemplate(id: string) {
    setTemplate(id)
    if (TEMPLATE_KINDS[id]) setKinds(TEMPLATE_KINDS[id])
    // Suggest the matching methodology pack.
    const suggested = methodologies.filter((m) => TEMPLATE_KINDS[id]?.includes(m.tech) || m.id === id).map((m) => m.id)
    if (suggested.length) setAdopt(suggested)
  }

  const toggle = (arr: string[], v: string) => (arr.includes(v) ? arr.filter((x) => x !== v) : [...arr, v])
  const scopeSeeds = useMemo<ScopeSeed[]>(() => [
    ...inScope.map((v) => ({ kind: inferKind(v), value: v, disposition: 'allow' })),
    ...outScope.map((v) => ({ kind: inferKind(v), value: v, disposition: 'deny' })),
  ], [inScope, outScope])

  async function submit() {
    if (!name.trim()) { setError('Give the engagement a name.'); return }
    setBusy(true)
    setError(null)
    try {
      const engagement: Engagement = {
        project_id: '', kinds, objective: objective.trim(), reference: reference.trim(),
        environment, data_class: dataClass, authorized, authorizer: authorizer.trim(), auth_to: authTo,
        techniques, window_start: windowStart, window_end: windowEnd, report_due: reportDue,
        standard, compliance, severity_scale: severity,
        contacts: contacts.filter((c) => c.name || c.email),
        test_accounts: testAccounts.filter((a) => a.username || a.role),
      }
      const project = await api.createEngagement({ name: name.trim(), engagement, scope: scopeSeeds })
      // Kickstart (best-effort — never block the created project on a seed failure).
      try {
        for (const id of adopt) await api.adoptMethodology(project.id, id)
        const appName = templates.find((t) => t.id === template)?.default_application || (firstRepo ? 'app' : '')
        if (appName || firstRepo) {
          const app = await api.createApplication(project.id, appName || 'app')
          if (firstRepo.trim()) await api.createAsset(app.id, 'source_repo', firstRepo.trim(), 'private')
        }
      } catch { /* seeds are optional; the project exists */ }
      onCreated(project)
    } catch (e) {
      setError((e as Error).message)
      setBusy(false)
    }
  }

  const addContact = () => setContacts([...contacts, { role: 'technical', name: '' }])
  const addAccount = () => setTestAccounts([...testAccounts, { role: 'user', username: '' }])

  return (
    <div className="em-backdrop" onClick={onClose}>
      <div className="em-modal" onClick={(e) => e.stopPropagation()}>
        <div className="em-head">
          <span className="em-dot" />
          <span className="em-title">New engagement</span>
          <button className="em-x" onClick={onClose}>esc ✕</button>
        </div>

        <div className="em-body">
          {error && <div className="banner error">⚠ {error}</div>}

          {/* 1 ESSENTIALS */}
          <section className="em-sect">
            <div className="em-sh"><span className="em-n">1</span><span className="em-t req">Essentials</span><span className="em-note">required</span></div>
            <div className="em-field">
              <label>Engagement name</label>
              <input ref={nameRef} className="em-in" value={name} onChange={(e) => setName(e.target.value)} placeholder="Acme Storefront — Q3 Web + API Assessment" />
            </div>
            <div className="em-field">
              <label>Assessment type</label>
              <div className="em-chiprow">
                {KINDS.map((t) => (
                  <button key={t.k} className={`em-chip ${kinds.includes(t.k) ? 'on' : ''}`} onClick={() => setKinds(toggle(kinds, t.k))}>{t.label}</button>
                ))}
              </div>
            </div>
            <div className="em-field">
              <label>Objective &amp; success criteria</label>
              <textarea className="em-in" rows={2} value={objective} onChange={(e) => setObjective(e.target.value)} placeholder="What are we assessing, and what does done look like?" />
            </div>
            <div className="em-field">
              <label>Reference <span className="em-opt">SOW / ticket</span></label>
              <input className="em-in" value={reference} onChange={(e) => setReference(e.target.value)} placeholder="SOW-2026-0417 / JIRA SEC-812" />
            </div>
          </section>

          {/* 2 SCOPE & AUTHORIZATION */}
          <section className="em-sect">
            <div className="em-sh"><span className="em-n">2</span><span className="em-t req">Scope &amp; authorization</span><span className="em-note">enforced</span></div>
            <div className="em-field">
              <label>In scope</label>
              <ScopeInput tokens={inScope} onChange={setInScope} />
            </div>
            <div className="em-field">
              <label>Out of scope — do not touch</label>
              <ScopeInput tokens={outScope} onChange={setOutScope} deny />
            </div>
            <div className="em-two">
              <div className="em-field">
                <label>Environment</label>
                <div className="em-seg">
                  {['production', 'staging', 'dev'].map((e) => (
                    <button key={e} className={environment === e ? 'on' : ''} onClick={() => setEnvironment(e)}>{e === 'production' ? 'Prod' : e === 'staging' ? 'Staging' : 'Dev'}</button>
                  ))}
                </div>
              </div>
              <div className="em-field">
                <label>Data sensitivity <span className="em-opt">gates external AI</span></label>
                <div className="em-seg">
                  {['open', 'private', 'restricted'].map((d) => (
                    <button key={d} className={dataClass === d ? 'on' : ''} onClick={() => setDataClass(d)}>{d[0].toUpperCase() + d.slice(1)}</button>
                  ))}
                </div>
              </div>
            </div>
            <div className="em-field">
              <label className="em-check" onClick={() => setAuthorized(!authorized)}>
                <span className={`em-box ${authorized ? 'on' : ''}`}>{authorized ? '✓' : ''}</span>
                Written authorization is on file for these targets
              </label>
              {authorized && (
                <div className="em-two" style={{ marginTop: 8 }}>
                  <input className="em-in" value={authorizer} onChange={(e) => setAuthorizer(e.target.value)} placeholder="Authorizer (name / email)" />
                  <label className="em-inline">valid until <input className="em-in" type="date" value={authTo} onChange={(e) => setAuthTo(e.target.value)} /></label>
                </div>
              )}
            </div>
            <div className="em-field">
              <label>Allowed techniques</label>
              <div className="em-toggles">
                {TECHNIQUES.map((t) => (
                  <button key={t.k} className="em-tog" onClick={() => setTechniques({ ...techniques, [t.k]: !techniques[t.k] })}>
                    <span className={`em-sw ${techniques[t.k] ? 'on' : ''}`}><i /></span> {t.label}
                  </button>
                ))}
              </div>
            </div>
          </section>

          {/* 3 KICKSTART */}
          <section className="em-sect">
            <div className="em-sh"><span className="em-n">3</span><span className="em-t">Kickstart</span><span className="em-note">so you can run day one</span></div>
            <div className="em-two">
              <div className="em-field">
                <label>Archetype <span className="em-opt">preset</span></label>
                <select className="em-in" value={template} onChange={(e) => applyTemplate(e.target.value)}>
                  <option value="">None</option>
                  {templates.filter((t) => t.id !== 'blank').map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
                </select>
              </div>
              <div className="em-field">
                <label>First repo / URL <span className="em-opt">→ asset</span></label>
                <input className="em-in" value={firstRepo} onChange={(e) => setFirstRepo(e.target.value)} placeholder="git@github.com:acme/storefront" />
              </div>
            </div>
            {methodologies.length > 0 && (
              <div className="em-field">
                <label>Adopt methodology now</label>
                <div className="em-chiprow">
                  {methodologies.map((m) => (
                    <button key={m.id} className={`em-chip ${adopt.includes(m.id) ? 'on' : ''}`} onClick={() => setAdopt(toggle(adopt, m.id))}>{adopt.includes(m.id) ? '✓ ' : ''}{m.title}</button>
                  ))}
                </div>
              </div>
            )}
            <div className="em-field">
              <label>Link issue tracker</label>
              <div className="em-chiprow">
                {['jira', 'defectdojo', ''].map((t) => (
                  <button key={t || 'none'} className={`em-chip ${tracker === t ? 'on' : ''}`} onClick={() => setTracker(t)}>{t === 'jira' ? 'Jira' : t === 'defectdojo' ? 'DefectDojo' : 'None'}</button>
                ))}
              </div>
              {tracker && <div className="em-hint">You'll finish connecting {tracker === 'jira' ? 'Jira' : 'DefectDojo'} in the project's Integrations tab.</div>}
            </div>
          </section>

          {/* ADVANCED */}
          <section className="em-sect">
            <button className="em-adv-h" onClick={() => setShowAdv(!showAdv)}>
              Advanced — timeline · contacts · access · reporting <span className="car">{showAdv ? '▾' : '▸'}</span>
            </button>
            {showAdv && (
              <div className="em-adv">
                <div className="em-sub">Timeline</div>
                <div className="em-three">
                  <label className="em-inline">Start <input className="em-in" type="date" value={windowStart} onChange={(e) => setWindowStart(e.target.value)} /></label>
                  <label className="em-inline">End <input className="em-in" type="date" value={windowEnd} onChange={(e) => setWindowEnd(e.target.value)} /></label>
                  <label className="em-inline">Report due <input className="em-in" type="date" value={reportDue} onChange={(e) => setReportDue(e.target.value)} /></label>
                </div>

                <div className="em-sub">Standard &amp; rating</div>
                <div className="em-three">
                  <input className="em-in" value={standard} onChange={(e) => setStandard(e.target.value)} placeholder="Standard (OWASP WSTG, ASVS L2…)" />
                  <input className="em-in" value={compliance} onChange={(e) => setCompliance(e.target.value)} placeholder="Compliance (PCI, SOC2…)" />
                  <select className="em-in" value={severity} onChange={(e) => setSeverity(e.target.value)}>
                    <option value="cvss31">CVSS 3.1</option><option value="cvss40">CVSS 4.0</option><option value="custom">Custom</option>
                  </select>
                </div>

                <div className="em-sub">Contacts <button className="em-add" onClick={addContact}>+ add</button></div>
                {contacts.map((c, i) => (
                  <div key={i} className="em-three" style={{ marginBottom: 6 }}>
                    <select className="em-in" value={c.role} onChange={(e) => setContacts(contacts.map((x, j) => j === i ? { ...x, role: e.target.value } : x))}>
                      <option value="technical">Technical POC</option><option value="authorizer">Authorizer</option><option value="breakglass">Break-glass</option>
                    </select>
                    <input className="em-in" value={c.name} onChange={(e) => setContacts(contacts.map((x, j) => j === i ? { ...x, name: e.target.value } : x))} placeholder="Name" />
                    <input className="em-in" value={c.email ?? ''} onChange={(e) => setContacts(contacts.map((x, j) => j === i ? { ...x, email: e.target.value } : x))} placeholder="Email / phone" />
                  </div>
                ))}

                <div className="em-sub">Test accounts <button className="em-add" onClick={addAccount}>+ add</button></div>
                {testAccounts.map((a, i) => (
                  <div key={i} className="em-three" style={{ marginBottom: 6 }}>
                    <input className="em-in" value={a.role} onChange={(e) => setTestAccounts(testAccounts.map((x, j) => j === i ? { ...x, role: e.target.value } : x))} placeholder="Role (admin/user/anon)" />
                    <input className="em-in" value={a.username ?? ''} onChange={(e) => setTestAccounts(testAccounts.map((x, j) => j === i ? { ...x, username: e.target.value } : x))} placeholder="Username" />
                    <input className="em-in" value={a.secret_ref ?? ''} onChange={(e) => setTestAccounts(testAccounts.map((x, j) => j === i ? { ...x, secret_ref: e.target.value } : x))} placeholder="Vault secret ref" />
                  </div>
                ))}
                <div className="em-hint">Passwords live in the vault — store only a secret reference here.</div>
              </div>
            )}
          </section>
        </div>

        <div className="em-foot">
          <button className="em-btn" onClick={onClose}>Cancel</button>
          <span className="em-sp" />
          <button className="em-btn pri" disabled={!online || busy || !name.trim()} onClick={submit}>
            {busy ? 'Creating…' : 'Create engagement'}
          </button>
        </div>
      </div>
    </div>
  )
}
