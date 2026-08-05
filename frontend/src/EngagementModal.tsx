import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { api, Engagement, EngagementContact, EngagementTestAccount, Group, Methodology, Organization, Project, ScopeSeed, setActiveProject } from './api'
import { hasNativePickers, pickDirectory, workingDir } from './native'

// The engagement setup modal (ADR-0051): create a project with its properties in one place instead of a bare
// name field. Captures the frame of an assessment — identity, scope + authorization, rules of engagement,
// kickstart, and (collapsed) the long tail — then creates the project with its engagement record + scope in
// one call, and best-effort seeds methodology adoption + a first asset.

// Each assessment type also says whether it involves live/dynamic testing (`active`). Active types get the
// network-scope + rules-of-engagement sections; static/advisory ones (code audit, secrets, threat model)
// don't — that's what keeps the form from reading pentest-heavy for a code review. `tech` maps to a
// methodology pack so the type drives kickstart (there is no separate archetype picker).
export const KINDS: { k: string; label: string; active: boolean; tech?: string }[] = [
  { k: 'web', label: 'Web app', active: true, tech: 'web' },
  { k: 'api', label: 'REST API', active: true, tech: 'api' },
  { k: 'graphql', label: 'GraphQL', active: true, tech: 'api' },
  { k: 'mobile', label: 'Mobile', active: true },
  { k: 'cloud', label: 'Cloud', active: true },
  { k: 'network', label: 'Network / infra', active: true },
  { k: 'red-team', label: 'Red team', active: true },
  { k: 'code', label: 'Code / SAST', active: false },
  { k: 'secrets', label: 'Secrets audit', active: false },
  { k: 'threat-model', label: 'Threat model', active: false },
]
export const TECHNIQUES = [
  { k: 'intrusive', label: 'Intrusive scanning' }, { k: 'automated_exploit', label: 'Automated exploitation' },
  { k: 'brute_force', label: 'Brute force / cred stuffing' }, { k: 'dos', label: 'DoS / stress' },
  { k: 'social', label: 'Social engineering' }, { k: 'destructive', label: 'Destructive / data-altering' },
]

function inferKind(value: string): string {
  if (value.includes('/')) return 'cidr'
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) return 'host'
  return 'domain'
}

// relRepo turns a picked absolute path into one relative to the base folder when it lives inside it, so the
// asset stays portable (resolved against base_path server-side). A path outside the base folder — or no base
// folder — stays absolute; both forms are supported downstream.
function relRepo(base: string, abs: string): string {
  if (!base) return abs
  const b = base.replace(/\/+$/, '')
  if (abs === b) return '.'
  return abs.startsWith(b + '/') ? abs.slice(b.length + 1) : abs
}

// repoKind classifies a first-repo value so the form can confirm how it will be stored. A remote URL
// (git/https) is left as-is; a leading "/" is absolute; anything else is relative to the base folder.
function repoKind(value: string): 'url' | 'absolute' | 'relative' {
  const s = value.trim()
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(s) || /^git@/.test(s)) return 'url'
  return s.startsWith('/') ? 'absolute' : 'relative'
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
  onClose,
  onCreated,
}: {
  online: boolean
  onClose: () => void
  onCreated: (p: Project) => void
}) {
  const [name, setName] = useState('')
  // Organization + team the project belongs to — drives KB inheritance (ADR-0041). Both optional.
  const [orgs, setOrgs] = useState<Organization[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [orgId, setOrgId] = useState('')
  const [groupId, setGroupId] = useState('')
  const [newOrg, setNewOrg] = useState('')
  const [newGroup, setNewGroup] = useState('')
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
  const [basePath, setBasePath] = useState('')
  // Opt-in: keep this project's OpenSecBench files in a `.opensecbench/` folder under the base folder —
  // one place the user controls, alongside their source (the dir-local model) — instead of the global data
  // dir. Off by default; only meaningful once a base folder is set.
  const [customLoc, setCustomLoc] = useState(false)
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
  // If the project is created but its optional kickstart seeding fails, we stash the project + a warning
  // rather than swallowing it, so the user can read what went wrong and still proceed into the project.
  const [created, setCreated] = useState<Project | null>(null)
  const [seedWarn, setSeedWarn] = useState<string | null>(null)
  const nameRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    nameRef.current?.focus()
    if (online) api.listMethodologies().then((m) => setMethodologies(m ?? [])).catch(() => {})
    const onKey = (e: globalThis.KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])
  useEffect(() => {
    if (online) api.listOrganizations().then((o) => setOrgs(o ?? [])).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])
  // Default the base folder to where the app was launched — a real, editable path beats a fake placeholder —
  // and point the first repo at that base folder (".") so a base folder alone yields a scannable asset.
  useEffect(() => {
    workingDir().then((wd) => { if (wd) { setBasePath((cur) => cur || wd); setFirstRepo((cur) => cur || '.') } }).catch(() => {})
  }, [])
  // Reload the org's teams (and reset the selection) whenever the chosen org changes.
  useEffect(() => {
    setGroupId('')
    if (online && orgId) api.listGroups(orgId).then((g) => setGroups(g ?? [])).catch(() => setGroups([]))
    else setGroups([])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, orgId])

  // The assessment type drives the form: active types reveal network scope + rules of engagement, and their
  // methodology packs are suggested for adoption. No separate archetype picker.
  const hasActive = useMemo(() => kinds.some((k) => KINDS.find((x) => x.k === k)?.active), [kinds])
  useEffect(() => {
    const techs = new Set(kinds.map((k) => KINDS.find((x) => x.k === k)?.tech).filter(Boolean))
    setAdopt(methodologies.filter((m) => techs.has(m.tech)).map((m) => m.id))
  }, [kinds, methodologies])

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
        project_id: '', base_path: basePath.trim(), kinds, objective: objective.trim(), reference: reference.trim(),
        environment, data_class: dataClass, authorized, authorizer: authorizer.trim(), auth_to: authTo,
        techniques, window_start: windowStart, window_end: windowEnd, report_due: reportDue,
        standard, compliance, severity_scale: severity,
        contacts: contacts.filter((c) => c.name || c.email),
        test_accounts: testAccounts.filter((a) => a.username || a.role),
      }
      const project = await api.createEngagement({ name: name.trim(), organization_id: orgId || null, group_id: groupId || null, engagement, scope: hasActive ? scopeSeeds : [], location: customLoc && basePath.trim() ? basePath.trim() : '' })
      // Scope subsequent requests to the new project so kickstart writes land in its database. In split mode
      // (ADR-0049) flat routes like POST /applications/{id}/assets resolve the per-project DB from the
      // X-Project-Id header; without this the seeds hit the wrong/empty DB and fail the FK to the app/project.
      setActiveProject(project.id)
      // Kickstart is best-effort — the project already exists — but surface a failure instead of swallowing
      // it, so a project doesn't silently come up with no assets/checklists. Stash the project and let the
      // user proceed into it once they've seen the error.
      try {
        for (const id of adopt) await api.adoptMethodology(project.id, id)
        if (firstRepo.trim()) {
          const app = await api.createApplication(project.id, kinds[0] || 'app')
          // An http(s) URL is a live web target (web_service, ADR-0067); a git remote / path is source code.
          const assetType = /^https?:\/\//i.test(firstRepo.trim()) ? 'web_service' : 'source_repo'
          await api.createAsset(app.id, assetType, firstRepo.trim(), 'private')
        }
      } catch (e) {
        setCreated(project)
        setSeedWarn(`Project created, but kickstart seeding failed: ${(e as Error).message}. Finish in the project's Assets / Checklist tabs.`)
        setBusy(false)
        return
      }
      onCreated(project)
    } catch (e) {
      setError((e as Error).message)
      setBusy(false)
    }
  }

  const addContact = () => setContacts([...contacts, { role: 'technical', name: '' }])
  const addAccount = () => setTestAccounts([...testAccounts, { role: 'user', username: '' }])
  async function addOrg() {
    const n = newOrg.trim()
    if (!n) return
    try {
      const o = await api.createOrganization(n)
      setOrgs((os) => [...os, o])
      setOrgId(o.id)
      setNewOrg('')
    } catch (e) {
      setError((e as Error).message)
    }
  }
  async function addGroup() {
    const n = newGroup.trim()
    if (!n || !orgId) return
    try {
      const g = await api.createGroup(orgId, n)
      setGroups((gs) => [...gs, g])
      setGroupId(g.id)
      setNewGroup('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

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
          {seedWarn && <div className="banner warn">⚠ {seedWarn}</div>}

          {/* 1 ESSENTIALS */}
          <section className="em-sect">
            <div className="em-sh"><span className="em-n">1</span><span className="em-t req">Essentials</span><span className="em-note">required</span></div>
            <div className="em-field">
              <label>Engagement name</label>
              <input ref={nameRef} className="em-in" value={name} onChange={(e) => setName(e.target.value)} placeholder="Acme Storefront — Q3 Web + API Assessment" />
            </div>
            <div className="em-field">
              <label>Organization &amp; team <span className="em-opt">shares knowledge across the team's projects</span></label>
              <div className="em-orggroup">
                <select className="em-in" value={orgId} onChange={(e) => setOrgId(e.target.value)}>
                  <option value="">— No organization —</option>
                  {orgs.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
                </select>
                <input
                  className="em-in"
                  value={newOrg}
                  onChange={(e) => setNewOrg(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); void addOrg() } }}
                  placeholder="＋ new organization"
                />
                <button type="button" className="em-add" onClick={() => void addOrg()} disabled={!newOrg.trim()}>Add</button>
              </div>
              {orgId && (
                <div className="em-orggroup">
                  <select className="em-in" value={groupId} onChange={(e) => setGroupId(e.target.value)}>
                    <option value="">— No team —</option>
                    {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
                  </select>
                  <input
                    className="em-in"
                    value={newGroup}
                    onChange={(e) => setNewGroup(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); void addGroup() } }}
                    placeholder="＋ new team"
                  />
                  <button type="button" className="em-add" onClick={() => void addGroup()} disabled={!newGroup.trim()}>Add</button>
                </div>
              )}
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

          {/* 2 SCOPE & AUTHORIZATION — adapts to the assessment type */}
          <section className="em-sect">
            <div className="em-sh">
              <span className="em-n">2</span>
              <span className="em-t req">{hasActive ? 'Scope & authorization' : 'Authorization & data handling'}</span>
              <span className="em-note">{hasActive ? 'enforced' : ''}</span>
            </div>
            {hasActive && (
              <>
                <div className="em-field">
                  <label>In-scope targets <span className="em-opt">host · domain · CIDR</span></label>
                  <ScopeInput tokens={inScope} onChange={setInScope} />
                </div>
                <div className="em-field">
                  <label>Out of scope — do not touch</label>
                  <ScopeInput tokens={outScope} onChange={setOutScope} deny />
                </div>
              </>
            )}
            <div className="em-two">
              {hasActive && (
                <div className="em-field">
                  <label>Environment</label>
                  <div className="em-seg">
                    {['production', 'staging', 'dev'].map((e) => (
                      <button key={e} className={environment === e ? 'on' : ''} onClick={() => setEnvironment(e)}>{e === 'production' ? 'Prod' : e === 'staging' ? 'Staging' : 'Dev'}</button>
                    ))}
                  </div>
                </div>
              )}
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
                Written authorization is on file for this work
              </label>
              {authorized && (
                <div className="em-two" style={{ marginTop: 8 }}>
                  <input className="em-in" value={authorizer} onChange={(e) => setAuthorizer(e.target.value)} placeholder="Authorizer (name / email)" />
                  <label className="em-inline">valid until <input className="em-in" type="date" value={authTo} onChange={(e) => setAuthTo(e.target.value)} /></label>
                </div>
              )}
            </div>
            {hasActive && (
              <div className="em-field">
                <label>Allowed techniques <span className="em-opt">disallowed ones are blocked</span></label>
                <div className="em-toggles">
                  {TECHNIQUES.map((t) => (
                    <button key={t.k} className="em-tog" onClick={() => setTechniques({ ...techniques, [t.k]: !techniques[t.k] })}>
                      <span className={`em-sw ${techniques[t.k] ? 'on' : ''}`}><i /></span> {t.label}
                    </button>
                  ))}
                </div>
              </div>
            )}
          </section>

          {/* 3 KICKSTART */}
          <section className="em-sect">
            <div className="em-sh"><span className="em-n">3</span><span className="em-t">Kickstart</span><span className="em-note">so you can run day one</span></div>
            <div className="em-field">
              <label>Base folder <span className="em-opt">project root on disk — assets resolve against it</span></label>
              <div className="em-browse">
                <input className="em-in" value={basePath} onChange={(e) => setBasePath(e.target.value)} placeholder="/home/you/src/acme" />
                {hasNativePickers() && (
                  <button className="em-btn" onClick={async () => { const p = await pickDirectory(basePath.trim() || undefined); if (p) setBasePath(p) }}>📁 Browse…</button>
                )}
              </div>
            </div>
            <div className="em-field">
              <label className={`em-check ${basePath.trim() ? '' : 'em-check-off'}`}>
                <input type="checkbox" checked={customLoc && !!basePath.trim()} disabled={!basePath.trim()} onChange={(e) => setCustomLoc(e.target.checked)} />
                Keep this project&apos;s files here <span className="em-opt">in a <code>.opensecbench</code> folder under the base folder</span>
              </label>
              <div className="em-hint">
                {!basePath.trim()
                  ? <>Set a base folder above to store this project alongside your source. Otherwise it goes in the default app data directory.</>
                  : customLoc
                    ? <>Stored in <code>{basePath.trim().replace(/\/+$/, '')}/.opensecbench/</code> — one folder, alongside your source. Deleting the project removes only that folder.</>
                    : <>Off: this project (database + evidence) goes in the default app data directory.</>}
              </div>
            </div>
            <div className="em-field">
              <label>First {hasActive ? 'repo or base URL' : 'repository'} <span className="em-opt">→ asset{basePath ? ', relative to base folder' : ''}</span></label>
              <div className="em-browse">
                <input className="em-in" value={firstRepo} onChange={(e) => setFirstRepo(e.target.value)} placeholder={basePath ? 'services/api  (relative to base folder)' : hasActive ? 'git@github.com:acme/storefront  or  https://shop.acme.com' : '/home/you/src/acme/repo'} />
                {hasNativePickers() && (
                  <button className="em-btn" onClick={async () => { const p = await pickDirectory(basePath.trim() || undefined); if (p) setFirstRepo(relRepo(basePath.trim(), p)) }}>📁</button>
                )}
              </div>
              {firstRepo.trim() && repoKind(firstRepo) === 'relative' && (
                <div className="em-hint">{basePath.trim()
                  ? <>Relative path — resolves to <code>{basePath.trim().replace(/\/+$/, '')}/{firstRepo.trim().replace(/^\.\/?/, '')}</code>.</>
                  : <>Relative path — set a base folder above so it resolves.</>}</div>
              )}
              {firstRepo.trim() && repoKind(firstRepo) === 'absolute' && (
                <div className="em-hint">Absolute path{basePath.trim() ? ' — outside the base folder, stored as-is.' : '.'}</div>
              )}
              {firstRepo.trim() && /^https?:\/\//i.test(firstRepo.trim()) && (
                <div className="em-hint">Live web target — added as a <code>web_service</code> asset (scannable, scope-checked).</div>
              )}
            </div>
            {methodologies.length > 0 && (
              <div className="em-field">
                <label>Add checklists now</label>
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
          {created
            ? <button className="em-btn pri" onClick={() => onCreated(created)}>Continue to project →</button>
            : <button className="em-btn pri" disabled={!online || busy || !name.trim()} onClick={submit}>
                {busy ? 'Creating…' : 'Create engagement'}
              </button>}
        </div>
      </div>
    </div>
  )
}
