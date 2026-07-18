import { Component, lazy, Suspense, useEffect, useMemo, useState, type FormEvent, type ChangeEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react'
import {
  api,
  Application,
  Asset,
  CapabilityManifest,
  ContextItem,
  CoverageView,
  AuditEvent,
  Finding,
  HTTPExchange,
  Observation,
  Playbook,
  ProxyStatus,
  Report,
  ReportTemplate,
  PlaybookRunResult,
  Project,
  ScopeEntry,
  TaskOutcome,
} from './api'
import { AnalystPanel } from './AnalystPanel'
import { NotificationBell } from './NotificationBell'
import { GraphTab } from './GraphTab'
import { KnowledgeTab } from './KnowledgeTab'
import { MethodologyTab } from './MethodologyTab'
import { TasksTab } from './TasksTab'
import { hasNativePickers, hasNativeBrowserLaunch, openProxyBrowser, pickDirectory } from './native'

// The terminal pulls in xterm.js; load it only when the tab is opened.
const TerminalTab = lazy(() => import('./TerminalTab').then((m) => ({ default: m.TerminalTab })))

type Tab =
  | 'assets'
  | 'methodology'
  | 'knowledge'
  | 'context'
  | 'scope'
  | 'scan'
  | 'repeater'
  | 'proxy'
  | 'terminal'
  | 'playbooks'
  | 'tasks'
  | 'findings'
  | 'reports'
  | 'graph'
  | 'audit'

type Conn = 'connecting' | 'online' | 'offline'

interface AppAssets {
  app: Application
  assets: Asset[]
}

// The activity bar surfaces (ADR-0015). The Analyst is not here — it is the
// right-hand dock, always present, never a surface you navigate to.
const SURFACES: { key: Tab; icon: string; label: string; meta?: boolean }[] = [
  { key: 'assets', icon: '🗂', label: 'Assets' },
  { key: 'methodology', icon: '✓', label: 'Method' },
  { key: 'knowledge', icon: '📚', label: 'Know' },
  { key: 'context', icon: '🔬', label: 'Context' },
  { key: 'findings', icon: '⚑', label: 'Find' },
  { key: 'repeater', icon: '↔', label: 'Repeat' },
  { key: 'proxy', icon: '📡', label: 'Proxy' },
  { key: 'terminal', icon: '▤', label: 'Term' },
  { key: 'scan', icon: '▷', label: 'Scan' },
  { key: 'playbooks', icon: '🧩', label: 'Play' },
  { key: 'tasks', icon: '☰', label: 'Tasks' },
  { key: 'graph', icon: '📊', label: 'Graph' },
  { key: 'scope', icon: '🛡', label: 'Scope' },
  { key: 'reports', icon: '📄', label: 'Report', meta: true },
  { key: 'audit', icon: '📜', label: 'Audit', meta: true },
]

function surfaceTitle(t: Tab): string {
  if (t === 'assets') return 'Applications & Assets'
  if (t === 'scan') return 'Scan'
  return t[0].toUpperCase() + t.slice(1)
}

// A crash in one surface must never blank the shell, the docked Analyst, or the
// status bar. Re-keyed per surface so switching away and back clears the error.
class SurfaceBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null }
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  render() {
    if (this.state.error) {
      return (
        <div className="banner error wb-banner">
          ⚠ This surface hit an error: {this.state.error.message}. Other surfaces and the Analyst are
          unaffected — switch away and back to retry.
        </div>
      )
    }
    return this.props.children
  }
}

// ── Contextual explorer (ADR-0015 Phase 2) ──────────────────────────────
// Left panel between the activity bar and the document center. Its content is
// scoped to the active surface, using data already loaded by the Workbench —
// no per-surface refetch, no surface refactor. Rows can jump between surfaces.

function packPct(items: { status: string }[]): number {
  if (!items.length) return 0
  return Math.round((items.filter((i) => i.status === 'covered').length / items.length) * 100)
}

const ASSET_ICON: Record<string, string> = {
  source_repo: '🗄',
  cloud_deployment: '☁',
  infrastructure: '🖧',
  document: '📄',
  correspondence: '✉',
}

const SEVERITIES = ['critical', 'high', 'medium', 'low', 'info']

function explorerTitle(t: Tab | null): string {
  if (t === 'methodology') return 'Coverage'
  if (t === 'assets') return 'Applications'
  if (t === 'findings') return 'Findings'
  return 'Project'
}

function WorkbenchExplorer({
  tab,
  project,
  apps,
  findings,
  coverage,
  onJump,
}: {
  tab: Tab | null
  project: Project
  apps: AppAssets[]
  findings: Finding[]
  coverage: CoverageView | null
  onJump: (t: Tab) => void
}) {
  return (
    <aside className="wb-explorer">
      <div className="wb-exp-head">
        {explorerTitle(tab)} <span className="r">{project.name}</span>
      </div>
      <div className="wb-exp-body">
        {tab === 'methodology' ? (
          !coverage || coverage.packs.length === 0 ? (
            <div className="wb-exp-empty">No methodology adopted yet.</div>
          ) : (
            coverage.packs.map((p) => {
              const pct = packPct(p.items)
              return (
                <div key={p.id} className="wb-exp-row">
                  <span className="lbl">{p.title}</span>
                  <span className="wb-exp-bar"><b style={{ width: `${pct}%` }} /></span>
                  <span className="pct">{pct}%</span>
                </div>
              )
            })
          )
        ) : tab === 'assets' ? (
          apps.length === 0 ? (
            <div className="wb-exp-empty">No applications yet.</div>
          ) : (
            apps.map((a) => (
              <div key={a.app.id}>
                <div className="wb-exp-row grp">📦 {a.app.name}</div>
                {a.assets.map((as) => (
                  <div key={as.id} className="wb-exp-row ind" title={as.location}>
                    <span className="ic">{ASSET_ICON[as.type] ?? '•'}</span>
                    <span className="lbl">{as.location}</span>
                  </div>
                ))}
              </div>
            ))
          )
        ) : tab === 'findings' ? (
          findings.length === 0 ? (
            <div className="wb-exp-empty">No findings yet.</div>
          ) : (
            SEVERITIES.map((sev) => {
              const n = findings.filter((f) => f.severity === sev).length
              if (!n) return null
              return (
                <div key={sev} className="wb-exp-row">
                  <span className={`sev sev-${sev}`}>{sev}</span>
                  <span className="pct">{n}</span>
                </div>
              )
            })
          )
        ) : (
          <div className="wb-exp-project">
            <div className="wb-exp-fact"><span className={`badge ${project.status}`}>{project.status}</span></div>
            <div className="wb-exp-fact">{apps.length} app{apps.length === 1 ? '' : 's'} · {findings.length} finding{findings.length === 1 ? '' : 's'}</div>
            {coverage && <div className="wb-exp-fact">Coverage {coverage.summary.covered_pct}%</div>}
            <div className="wb-exp-links">
              {(['methodology', 'assets', 'findings', 'repeater'] as Tab[]).map((t) => (
                <button key={t} onClick={() => onJump(t)}>{SURFACES.find((s) => s.key === t)?.icon} {surfaceTitle(t)}</button>
              ))}
            </div>
          </div>
        )}
      </div>
    </aside>
  )
}

export function Workbench({ project, conn, onHome }: { project: Project; conn: Conn; onHome: () => void }) {
  const online = conn === 'online'
  // Open documents are kept mounted so their state survives navigation (ADR-0015
  // Phase 3): switching surfaces hides the inactive ones, it never tears them down.
  const [openDocs, setOpenDocs] = useState<Tab[]>(['methodology']) // land on the coverage home
  const [activeDoc, setActiveDoc] = useState<Tab | null>('methodology')
  const [apps, setApps] = useState<AppAssets[]>([])
  const [capabilities, setCapabilities] = useState<CapabilityManifest[]>([])
  const [context, setContext] = useState<ContextItem[]>([])
  const [findings, setFindings] = useState<Finding[]>([])
  const [coverage, setCoverage] = useState<CoverageView | null>(null)
  const [approvals, setApprovals] = useState(0)
  const [error, setError] = useState<string | null>(null)

  async function loadApps() {
    const list = (await api.listApplications(project.id)) ?? []
    const withAssets = await Promise.all(
      list.map(async (app) => ({ app, assets: (await api.listAssets(app.id)) ?? [] })),
    )
    setApps(withAssets)
  }

  async function loadAll() {
    try {
      await loadApps()
      setCapabilities((await api.listCapabilities()) ?? [])
      setContext((await api.listContext(project.id)) ?? [])
      setFindings((await api.listFindings()) ?? [])
      setCoverage(await api.getMethodologyCoverage(project.id))
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  useEffect(() => {
    if (online) void loadAll()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  // Approvals feed the status bar, independent of the active surface.
  useEffect(() => {
    if (!online) return
    let alive = true
    const tick = () =>
      api
        .listApprovals()
        .then((a) => alive && setApprovals(a?.length ?? 0))
        .catch(() => {})
    void tick()
    const timer = setInterval(tick, 5000)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [online])

  const allAssets = useMemo(
    () => apps.flatMap((a) => a.assets.map((asset) => ({ asset, appName: a.app.name }))),
    [apps],
  )

  function openDoc(t: Tab) {
    setOpenDocs((docs) => (docs.includes(t) ? docs : [...docs, t]))
    setActiveDoc(t)
  }
  function closeDoc(t: Tab, e?: ReactMouseEvent) {
    e?.stopPropagation()
    const idx = openDocs.indexOf(t)
    const next = openDocs.filter((d) => d !== t)
    setOpenDocs(next)
    if (activeDoc === t) setActiveDoc(next[idx] ?? next[idx - 1] ?? null)
  }

  // Each open document renders its surface once and stays mounted; the frame only
  // toggles visibility. Adding a surface here makes it an openable document.
  function renderSurface(t: Tab) {
    switch (t) {
      case 'assets':
        return <AssetsTab project={project} apps={apps} online={online} reload={loadApps} onError={setError} />
      case 'methodology':
        return <MethodologyTab project={project} online={online} onError={setError} />
      case 'knowledge':
        return <KnowledgeTab project={project} online={online} onError={setError} />
      case 'context':
        return <ContextTab project={project} items={context} online={online} reload={async () => setContext((await api.listContext(project.id)) ?? [])} onError={setError} />
      case 'scope':
        return <ScopeTab project={project} online={online} onError={setError} />
      case 'scan':
        return <ScanTab assets={allAssets} capabilities={capabilities} online={online} afterFinding={loadAll} onError={setError} />
      case 'repeater':
        return <RepeaterTab project={project} online={online} onError={setError} />
      case 'proxy':
        return <ProxyTab project={project} online={online} onError={setError} />
      case 'terminal':
        return (
          <Suspense fallback={<div className="empty">Loading terminal…</div>}>
            <TerminalTab project={project} online={online} onError={setError} />
          </Suspense>
        )
      case 'playbooks':
        return <PlaybooksTab assets={allAssets} online={online} onError={setError} />
      case 'tasks':
        return <TasksTab online={online} onError={setError} />
      case 'findings':
        return <FindingsTab findings={findings} />
      case 'reports':
        return <ReportsTab project={project} online={online} onError={setError} />
      case 'graph':
        return <GraphTab project={project} online={online} onError={setError} />
      case 'audit':
        return <AuditTab online={online} onError={setError} />
    }
  }

  return (
    <div className="wb">
      <div className="wb-titlebar">
        <button className={`wb-proj ${online ? 'online' : ''}`} onClick={onHome} title="Back to Home">
          <span className="dot" /> {project.name} <span className="car">▾</span>
        </button>
        <div className="wb-omni" title="Omni-search — coming soon">
          <span>⌕</span> Search code · traffic · findings · knowledge…
          <kbd>⌘K</kbd>
        </div>
        <NotificationBell online={online} />
        <code className="apiurl">{api.baseURL}</code>
      </div>

      <div className="wb-body">
        <nav className="wb-activity">
          {SURFACES.filter((s) => !s.meta).map((s) => (
            <button key={s.key} className={`wb-ic ${activeDoc === s.key ? 'on' : ''} ${openDocs.includes(s.key) ? 'opened' : ''}`} title={surfaceTitle(s.key)} onClick={() => openDoc(s.key)}>
              <span>{s.icon}</span>
              {s.key === 'findings' && findings.length > 0 && <span className="n red">{findings.length}</span>}
              {s.key === 'context' && context.length > 0 && <span className="n">{context.length}</span>}
              <small>{s.label}</small>
            </button>
          ))}
          <div className="wb-actsp" />
          <div className="wb-actdiv" />
          {SURFACES.filter((s) => s.meta).map((s) => (
            <button key={s.key} className={`wb-ic ${activeDoc === s.key ? 'on' : ''} ${openDocs.includes(s.key) ? 'opened' : ''}`} title={surfaceTitle(s.key)} onClick={() => openDoc(s.key)}>
              <span>{s.icon}</span>
              <small>{s.label}</small>
            </button>
          ))}
        </nav>

        <WorkbenchExplorer tab={activeDoc} project={project} apps={apps} findings={findings} coverage={coverage} onJump={openDoc} />

        <div className="wb-center">
          <div className="wb-doctabs">
            {openDocs.map((t) => (
              <div key={t} className={`wb-doctab ${activeDoc === t ? 'on' : ''}`} onClick={() => setActiveDoc(t)} title={surfaceTitle(t)}>
                <span className="em">{SURFACES.find((s) => s.key === t)?.icon}</span>
                <span className="lbl">{surfaceTitle(t)}</span>
                <span className="x" title="Close" onClick={(e) => closeDoc(t, e)}>✕</span>
              </div>
            ))}
          </div>
          {error && <div className="banner error wb-banner">⚠ {error}</div>}
          <div className="wb-docarea">
            {openDocs.length === 0 && (
              <div className="empty">No document open — pick a surface from the activity bar on the left.</div>
            )}
            {openDocs.map((t) => (
              <div key={t} className="wb-doc" style={{ display: activeDoc === t ? 'block' : 'none' }}>
                <SurfaceBoundary>{renderSurface(t)}</SurfaceBoundary>
              </div>
            ))}
          </div>
        </div>

        <SurfaceBoundary>
          <AnalystPanel project={project} online={online} />
        </SurfaceBoundary>
      </div>

      <div className="wb-status">
        <span className={`b ${online ? 'good' : ''}`}>
          <span className="sdot" /> {online ? 'control plane online' : conn === 'offline' ? 'control plane offline' : 'connecting…'}
        </span>
        <span className="b good">⛨ egress governed</span>
        {coverage && coverage.summary.total > 0 && (
          <span className="b">coverage {coverage.summary.covered_pct}%</span>
        )}
        <span className="sp" />
        {approvals > 0 && (
          <span className="warnpill">⏸ {approvals} approval{approvals === 1 ? '' : 's'} waiting</span>
        )}
        <span className="b">audit ●</span>
      </div>
    </div>
  )
}

function ReportsTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [templates, setTemplates] = useState<ReportTemplate[]>([])
  const [reports, setReports] = useState<Report[]>([])
  const [template, setTemplate] = useState('technical')
  const [format, setFormat] = useState('html')
  const [busy, setBusy] = useState(false)

  async function reload() {
    try {
      setReports((await api.listReports(project.id)) ?? [])
    } catch (e) {
      onError((e as Error).message)
    }
  }

  useEffect(() => {
    if (!online) return
    void (async () => {
      try {
        const tmpls = (await api.listReportTemplates()) ?? []
        setTemplates(tmpls)
        if (tmpls.length && !tmpls.find((t) => t.id === template)) setTemplate(tmpls[0].id)
      } catch (e) {
        onError((e as Error).message)
      }
    })()
    void reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  async function generate() {
    setBusy(true)
    try {
      const rep = await api.generateReport(project.id, template, format)
      await reload()
      window.open(api.artifactContentURL(rep.artifact_id), '_blank')
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">Reports</div>
      <p className="hint">
        Generated from confirmed findings with traceable evidence only. PDF renders via a local
        headless browser.
      </p>
      <div className="create-row">
        <select value={template} onChange={(e) => setTemplate(e.target.value)}>
          {templates.map((t) => (
            <option key={t.id} value={t.id}>{t.title}</option>
          ))}
        </select>
        <select value={format} onChange={(e) => setFormat(e.target.value)}>
          {['html', 'md', 'pdf'].map((f) => (
            <option key={f} value={f}>{f.toUpperCase()}</option>
          ))}
        </select>
        <button onClick={generate} disabled={!online || busy}>
          {busy ? 'Generating…' : 'Generate'}
        </button>
      </div>
      {reports.length === 0 ? (
        <div className="empty">No reports generated yet.</div>
      ) : (
        <ul className="rows">
          {reports.map((rep) => (
            <li key={rep.id} className="row-item">
              <span className="badge">{rep.format}</span>
              <span className="row-title">{rep.title}</span>
              <span className="muted mono">{new Date(rep.created_at).toLocaleString()}</span>
              <a className="link" href={api.artifactContentURL(rep.artifact_id)} target="_blank" rel="noreferrer">open</a>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function AuditTab({ online, onError }: { online: boolean; onError: (m: string) => void }) {
  const [events, setEvents] = useState<AuditEvent[]>([])

  async function reload() {
    try {
      setEvents((await api.listAudit(200)) ?? [])
    } catch (e) {
      onError((e as Error).message)
    }
  }

  useEffect(() => {
    if (online) void reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  return (
    <section className="panel">
      <div className="panel-head">
        Audit trail
        <button className="link" onClick={reload}>refresh</button>
      </div>
      <p className="hint">Append-only, hash-chained record of every governed action.</p>
      {events.length === 0 ? (
        <div className="empty">No audit events yet.</div>
      ) : (
        <table className="audit-table">
          <thead>
            <tr>
              <th>#</th>
              <th>time</th>
              <th>action</th>
              <th>actor</th>
              <th>target</th>
            </tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e.seq} title={e.hash}>
                <td className="muted">{e.seq}</td>
                <td className="mono">{new Date(e.time).toLocaleString()}</td>
                <td><span className="badge">{e.action}</span></td>
                <td className="mono">{e.actor}</td>
                <td className="mono truncate">{e.target}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}

function AssetsTab({
  project,
  apps,
  online,
  reload,
  onError,
}: {
  project: Project
  apps: AppAssets[]
  online: boolean
  reload: () => Promise<void>
  onError: (m: string) => void
}) {
  const [appName, setAppName] = useState('')
  const [assetInputs, setAssetInputs] = useState<Record<string, { type: string; location: string; sensitivity: string }>>({})

  async function addApp(e: FormEvent) {
    e.preventDefault()
    if (!appName.trim()) return
    try {
      await api.createApplication(project.id, appName.trim())
      setAppName('')
      await reload()
    } catch (err) {
      onError((err as Error).message)
    }
  }

  async function addAsset(appId: string) {
    const inp = assetInputs[appId] || { type: 'source_repo', location: '', sensitivity: '' }
    if (!inp.location.trim()) return
    try {
      await api.createAsset(appId, inp.type || 'source_repo', inp.location.trim(), inp.sensitivity)
      setAssetInputs({ ...assetInputs, [appId]: { type: 'source_repo', location: '', sensitivity: '' } })
      await reload()
    } catch (err) {
      onError((err as Error).message)
    }
  }

  return (
    <div>
      <form className="create-row" onSubmit={addApp}>
        <input value={appName} onChange={(e) => setAppName(e.target.value)} placeholder="New application name…" disabled={!online} />
        <button type="submit" disabled={!online || !appName.trim()}>＋ Application</button>
      </form>

      {apps.length === 0 && <div className="empty">No applications yet.</div>}

      {apps.map(({ app, assets }) => {
        const inp = assetInputs[app.id] || { type: 'source_repo', location: '', sensitivity: '' }
        return (
          <section className="panel" key={app.id}>
            <div className="panel-head">📦 {app.name}</div>
            {assets.length === 0 ? (
              <div className="empty">No assets.</div>
            ) : (
              <ul className="rows">
                {assets.map((as) => (
                  <li key={as.id} className="row-item">
                    <span className="badge">{as.type}</span>
                    <span className={`sens sens-${as.sensitivity}`}>{as.sensitivity}</span>
                    <span className="mono">{as.location}</span>
                  </li>
                ))}
              </ul>
            )}
            <div className="create-row sub">
              <select value={inp.type} onChange={(e) => setAssetInputs({ ...assetInputs, [app.id]: { ...inp, type: e.target.value } })}>
                {['source_repo', 'cloud_deployment', 'infrastructure', 'document', 'correspondence'].map((t) => (
                  <option key={t} value={t}>{t}</option>
                ))}
              </select>
              <input placeholder="location / path…" value={inp.location} onChange={(e) => setAssetInputs({ ...assetInputs, [app.id]: { ...inp, location: e.target.value } })} />
              {hasNativePickers() && (inp.type === 'source_repo' || inp.type === 'document') && (
                <button
                  type="button"
                  className="ghost-btn"
                  onClick={async () => {
                    const p = await pickDirectory()
                    if (p) setAssetInputs({ ...assetInputs, [app.id]: { ...inp, location: p } })
                  }}
                >
                  Browse…
                </button>
              )}
              <select value={inp.sensitivity} onChange={(e) => setAssetInputs({ ...assetInputs, [app.id]: { ...inp, sensitivity: e.target.value } })}>
                <option value="">sensitivity: infer</option>
                <option value="private">private</option>
                <option value="open_source">open_source</option>
              </select>
              <button disabled={!online || !inp.location.trim()} onClick={() => addAsset(app.id)}>＋ Asset</button>
            </div>
          </section>
        )
      })}
    </div>
  )
}

function ContextTab({
  project,
  items,
  online,
  reload,
  onError,
}: {
  project: Project
  items: ContextItem[]
  online: boolean
  reload: () => Promise<void>
  onError: (m: string) => void
}) {
  const [type, setType] = useState('document')
  const [busy, setBusy] = useState(false)

  async function upload(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    setBusy(true)
    try {
      await api.ingestContext(project.id, file.name, type, file)
      e.target.value = ''
      await reload()
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">Context</div>
      <div className="create-row">
        <select value={type} onChange={(e) => setType(e.target.value)}>
          {['document', 'email', 'chat', 'note'].map((t) => (
            <option key={t} value={t}>{t}</option>
          ))}
        </select>
        <label className={`filebtn ${busy ? 'busy' : ''}`}>
          {busy ? 'Uploading…' : '＋ Add file'}
          <input type="file" onChange={upload} disabled={!online || busy} hidden />
        </label>
      </div>
      {items.length === 0 ? (
        <div className="empty">No context ingested.</div>
      ) : (
        <ul className="rows">
          {items.map((ci) => (
            <li key={ci.id} className="row-item">
              <span className="badge">{ci.type}</span>
              <span className="row-title">{ci.name}</span>
              <a className="link" href={api.artifactContentURL(ci.artifact_id)} target="_blank" rel="noreferrer">open</a>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

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

const HTTP_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

function RepeaterTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [history, setHistory] = useState<HTTPExchange[]>([])
  const [method, setMethod] = useState('GET')
  const [url, setUrl] = useState('')
  const [headers, setHeaders] = useState('')
  const [body, setBody] = useState('')
  const [current, setCurrent] = useState<HTTPExchange | null>(null)
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)

  async function reload() {
    setHistory((await api.listExchanges(project.id)) ?? [])
  }

  useEffect(() => {
    if (online) void reload().catch((e) => onError((e as Error).message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  function load(ex: HTTPExchange) {
    setMethod(ex.method)
    setUrl(ex.url)
    setHeaders(ex.request_headers)
    setBody(ex.request_body)
    setCurrent(ex)
    setSaved(false)
  }

  async function send(e: FormEvent) {
    e.preventDefault()
    if (!url.trim()) return
    setBusy(true)
    setSaved(false)
    try {
      const ex = await api.createExchange(project.id, {
        method,
        url: url.trim(),
        request_headers: headers,
        request_body: body,
      })
      const sent = await api.sendExchange(ex.id)
      setCurrent(sent)
      await reload()
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function saveEvidence() {
    if (!current) return
    try {
      await api.saveExchangeEvidence(current.id, '')
      setSaved(true)
    } catch (err) {
      onError((err as Error).message)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">Repeater</div>
      <p className="hint">
        Craft a request and send it. Targets are checked against the project scope allowlist before
        anything leaves the machine.
      </p>
      <div className="repeater">
        <form className="repeater-req" onSubmit={send}>
          <div className="repeater-line">
            <select value={method} onChange={(e) => setMethod(e.target.value)}>
              {HTTP_METHODS.map((m) => (
                <option key={m} value={m}>{m}</option>
              ))}
            </select>
            <input
              className="repeater-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://api.acme.com/v2/users"
              disabled={!online || busy}
            />
            <button type="submit" disabled={!online || busy || !url.trim()}>
              {busy ? 'Sending…' : 'Send'}
            </button>
          </div>
          <label className="repeater-label">Headers</label>
          <textarea
            className="mono"
            rows={4}
            value={headers}
            onChange={(e) => setHeaders(e.target.value)}
            placeholder={'Authorization: Bearer …\nContent-Type: application/json'}
          />
          <label className="repeater-label">Body</label>
          <textarea className="mono" rows={5} value={body} onChange={(e) => setBody(e.target.value)} />
        </form>

        <div className="repeater-res">
          {current && current.sent_at ? (
            <>
              <div className="repeater-status">
                <span className={`badge ${statusClass(current.status)}`}>{current.status ?? '—'}</span>
                {current.duration_ms != null && <span className="muted">{current.duration_ms} ms</span>}
                <button className="link" onClick={saveEvidence} disabled={saved}>
                  {saved ? '✓ saved as evidence' : 'save as evidence'}
                </button>
              </div>
              <pre className="mono response">{current.response_headers}</pre>
              <pre className="mono response body">{current.response_body}</pre>
            </>
          ) : (
            <div className="empty">Send a request to see its response.</div>
          )}
        </div>
      </div>

      {history.length > 0 && (
        <ul className="rows repeater-history">
          {history.map((ex) => (
            <li key={ex.id} className="row-item clickable" onClick={() => load(ex)}>
              <span className={`badge ${statusClass(ex.status)}`}>{ex.status ?? '—'}</span>
              <span className="kind">{ex.method}</span>
              <span className="row-title mono">{ex.url}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function statusClass(status?: number): string {
  if (status == null) return ''
  if (status >= 400) return 'failed'
  if (status >= 200 && status < 300) return 'succeeded'
  return 'active'
}

function ProxyTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [status, setStatus] = useState<ProxyStatus>({ running: false })
  const [captured, setCaptured] = useState<HTTPExchange[]>([])
  const [busy, setBusy] = useState(false)

  async function refresh() {
    try {
      setStatus(await api.getProxy(project.id))
      const all = (await api.listExchanges(project.id)) ?? []
      setCaptured(all.filter((e) => e.origin === 'proxy'))
    } catch (e) {
      onError((e as Error).message)
    }
  }

  useEffect(() => {
    if (!online) return
    void refresh()
    const timer = setInterval(refresh, 2500) // poll so captured traffic streams in
    return () => clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  async function toggle() {
    setBusy(true)
    try {
      setStatus(status.running ? await api.stopProxy(project.id) : await api.startProxy(project.id))
      await refresh()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">Intercepting proxy</div>
      <p className="hint">
        Route a browser or tool through the proxy to capture traffic (scope-guarded). For HTTPS,
        trust the CA below — it is generated locally and never installed automatically. Or skip the
        trust step entirely: <code>osb proxy browser --project {project.id.slice(0, 8)}…</code> launches
        a throwaway Chromium preconfigured to use this proxy and trust its CA.
      </p>
      <div className="term-toolbar">
        <button onClick={toggle} disabled={!online || busy}>
          {busy ? '…' : status.running ? 'Stop proxy' : 'Start proxy'}
        </button>
        {status.running && (
          <span className="mono muted">
            listening on <b>127.0.0.1:{status.port}</b>
          </span>
        )}
        {status.running && hasNativeBrowserLaunch() && status.ca_spki_sha256 && (
          <button
            onClick={() => {
              void openProxyBrowser(status.port ?? 0, status.ca_spki_sha256 ?? '').catch((e) => onError((e as Error).message))
            }}
          >
            Open browser
          </button>
        )}
        <a className="link" href={api.proxyCAURL()} target="_blank" rel="noreferrer">download CA cert</a>
      </div>
      {captured.length === 0 ? (
        <div className="empty">No captured traffic yet.</div>
      ) : (
        <table className="audit-table">
          <thead>
            <tr>
              <th>status</th>
              <th>method</th>
              <th>url</th>
              <th>ms</th>
            </tr>
          </thead>
          <tbody>
            {captured.map((e) => (
              <tr key={e.id}>
                <td><span className={`badge ${statusClass(e.status)}`}>{e.status ?? '—'}</span></td>
                <td className="kind">{e.method}</td>
                <td className="mono truncate">{e.url}</td>
                <td className="muted">{e.duration_ms ?? ''}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}

function ScanTab({
  assets,
  capabilities,
  online,
  afterFinding,
  onError,
}: {
  assets: { asset: Asset; appName: string }[]
  capabilities: CapabilityManifest[]
  online: boolean
  afterFinding: () => Promise<void>
  onError: (m: string) => void
}) {
  const repoAssets = assets.filter((a) => a.asset.type === 'source_repo')
  const [capId, setCapId] = useState('')
  const [assetId, setAssetId] = useState('')
  const [config, setConfig] = useState('')
  const [running, setRunning] = useState(false)
  const [outcome, setOutcome] = useState<TaskOutcome | null>(null)
  const [obsState, setObsState] = useState<Record<string, string>>({})
  const [findingTitle, setFindingTitle] = useState('')
  const [findingSeverity, setFindingSeverity] = useState('medium')

  async function run() {
    if (!capId || !assetId) return
    setRunning(true)
    setOutcome(null)
    try {
      const params: Record<string, unknown> = {}
      if (config.trim()) params.config = config.trim()
      const out = await api.runTask({ capability_id: capId, asset_id: assetId, params, actor: 'human' })
      setOutcome(out)
      const st: Record<string, string> = {}
      for (const o of out.observations) st[o.id] = o.review_state
      setObsState(st)
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setRunning(false)
    }
  }

  async function review(o: Observation, state: string) {
    try {
      await api.reviewObservation(o.id, state)
      setObsState((s) => ({ ...s, [o.id]: state }))
    } catch (err) {
      onError((err as Error).message)
    }
  }

  const confirmed = outcome?.observations.filter((o) => obsState[o.id] === 'confirmed') ?? []

  async function createFinding() {
    if (!findingTitle.trim() || confirmed.length === 0) return
    try {
      await api.createFinding({ title: findingTitle.trim(), severity: findingSeverity, observation_ids: confirmed.map((o) => o.id) })
      setFindingTitle('')
      await afterFinding()
      onError('') // clear
      alert('Finding created from ' + confirmed.length + ' observation(s).')
    } catch (err) {
      onError((err as Error).message)
    }
  }

  return (
    <div>
      <section className="panel">
        <div className="panel-head">Run a capability</div>
        <div className="create-row">
          <select value={capId} onChange={(e) => setCapId(e.target.value)}>
            <option value="">capability…</option>
            {capabilities.map((c) => (
              <option key={c.id} value={c.id}>{c.title}</option>
            ))}
          </select>
          <select value={assetId} onChange={(e) => setAssetId(e.target.value)}>
            <option value="">source-repo asset…</option>
            {repoAssets.map((a) => (
              <option key={a.asset.id} value={a.asset.id}>{a.appName}: {a.asset.location}</option>
            ))}
          </select>
          <input placeholder="param: config (optional)" value={config} onChange={(e) => setConfig(e.target.value)} />
          <button disabled={!online || running || !capId || !assetId} onClick={run}>{running ? 'Running…' : '▷ Run'}</button>
        </div>
        {repoAssets.length === 0 && <div className="empty">Add a source_repo asset first (Applications &amp; Assets).</div>}
      </section>

      {outcome && (
        <section className="panel">
          <div className="panel-head">
            Task <span className={`badge ${outcome.task.status}`}>{outcome.task.status}</span> · {outcome.task.runner} · {outcome.observations.length} observation(s)
          </div>
          {outcome.task.error && <div className="banner error">⚠ {outcome.task.error}</div>}
          {outcome.observations.length === 0 ? (
            <div className="empty">No observations from this run.</div>
          ) : (
            <ul className="rows">
              {outcome.observations.map((o) => (
                <li key={o.id} className="obs">
                  <span className={`sev sev-${o.severity}`}>{o.severity}</span>
                  <div className="obs-main">
                    <div className="obs-title">{o.title}</div>
                    <div className="muted mono">{o.rule_id} {o.location}</div>
                  </div>
                  <div className="obs-actions">
                    <span className={`state state-${obsState[o.id]}`}>{obsState[o.id]}</span>
                    <button className="mini ok" disabled={obsState[o.id] === 'confirmed'} onClick={() => review(o, 'confirmed')}>confirm</button>
                    <button className="mini no" disabled={obsState[o.id] === 'rejected'} onClick={() => review(o, 'rejected')}>reject</button>
                  </div>
                </li>
              ))}
            </ul>
          )}

          {confirmed.length > 0 && (
            <div className="create-row promote">
              <input placeholder="Finding title…" value={findingTitle} onChange={(e) => setFindingTitle(e.target.value)} />
              <select value={findingSeverity} onChange={(e) => setFindingSeverity(e.target.value)}>
                {['info', 'low', 'medium', 'high', 'critical'].map((s) => <option key={s} value={s}>{s}</option>)}
              </select>
              <button disabled={!findingTitle.trim()} onClick={createFinding}>⚑ Create finding from {confirmed.length} confirmed</button>
            </div>
          )}
        </section>
      )}
    </div>
  )
}

function PlaybooksTab({
  assets,
  online,
  onError,
}: {
  assets: { asset: Asset; appName: string }[]
  online: boolean
  onError: (m: string) => void
}) {
  const repoAssets = assets.filter((a) => a.asset.type === 'source_repo')
  const [playbooks, setPlaybooks] = useState<Playbook[]>([])
  const [pbId, setPbId] = useState('')
  const [assetId, setAssetId] = useState('')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<PlaybookRunResult | null>(null)

  useEffect(() => {
    if (!online) return
    api.listPlaybooks().then((p) => setPlaybooks(p ?? [])).catch((e) => onError((e as Error).message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  async function run() {
    if (!pbId || !assetId) return
    setRunning(true)
    setResult(null)
    try {
      setResult(await api.runPlaybook(pbId, assetId))
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setRunning(false)
    }
  }

  return (
    <div>
      <section className="panel">
        <div className="panel-head">Run a playbook</div>
        <div className="create-row">
          <select value={pbId} onChange={(e) => setPbId(e.target.value)}>
            <option value="">playbook…</option>
            {playbooks.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} [{p.steps.map((s) => s.capability).join(', ')}]
              </option>
            ))}
          </select>
          <select value={assetId} onChange={(e) => setAssetId(e.target.value)}>
            <option value="">source-repo asset…</option>
            {repoAssets.map((a) => (
              <option key={a.asset.id} value={a.asset.id}>
                {a.appName}: {a.asset.location}
              </option>
            ))}
          </select>
          <button disabled={!online || running || !pbId || !assetId} onClick={run}>
            {running ? 'Running…' : '▷ Run'}
          </button>
        </div>
        {repoAssets.length === 0 && <div className="empty">Add a source_repo asset first (Applications &amp; Assets).</div>}
      </section>

      {result && (
        <section className="panel">
          <div className="panel-head">
            Run <span className={`badge ${result.run.status}`}>{result.run.status}</span> · {result.outcomes.length} step(s)
          </div>
          <ul className="rows">
            {result.outcomes.map((o, i) => (
              <li key={o.task.id} className="row-item">
                <span className="muted">#{i + 1}</span>
                <span className={`badge ${o.task.status}`}>{o.task.status}</span>
                <span className="row-title">{o.task.capability_id}</span>
                <span className="muted">{o.observations.length} obs</span>
              </li>
            ))}
          </ul>
          <div className="empty" style={{ textAlign: 'left' }}>
            Review each step's output and triage observations in the Tasks tab.
          </div>
        </section>
      )}
    </div>
  )
}

function FindingsTab({ findings }: { findings: Finding[] }) {
  return (
    <section className="panel">
      <div className="panel-head">Findings ({findings.length})</div>
      {findings.length === 0 ? (
        <div className="empty">No findings yet. Run a scan and promote confirmed observations.</div>
      ) : (
        <ul className="rows">
          {findings.map((f) => (
            <li key={f.id} className="row-item">
              <span className={`sev sev-${f.severity}`}>{f.severity}</span>
              <span className={`badge ${f.status}`}>{f.status}</span>
              <span className="row-title">{f.title}</span>
              <span className="muted">{f.observation_ids.length} obs</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
