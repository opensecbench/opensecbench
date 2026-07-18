import { lazy, Suspense, useEffect, useMemo, useState, type FormEvent, type ChangeEvent } from 'react'
import {
  api,
  Application,
  Asset,
  CapabilityManifest,
  ContextItem,
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
import { GraphTab } from './GraphTab'
import { KnowledgeTab } from './KnowledgeTab'
import { MethodologyTab } from './MethodologyTab'
import { TasksTab } from './TasksTab'
import { hasNativePickers, pickDirectory } from './native'

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
  | 'analyst'
  | 'audit'

interface AppAssets {
  app: Application
  assets: Asset[]
}

export function Workbench({ project, online }: { project: Project; online: boolean }) {
  const [tab, setTab] = useState<Tab>('assets')
  const [apps, setApps] = useState<AppAssets[]>([])
  const [capabilities, setCapabilities] = useState<CapabilityManifest[]>([])
  const [context, setContext] = useState<ContextItem[]>([])
  const [findings, setFindings] = useState<Finding[]>([])
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
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  useEffect(() => {
    if (online) void loadAll()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  const allAssets = useMemo(
    () => apps.flatMap((a) => a.assets.map((asset) => ({ asset, appName: a.app.name }))),
    [apps],
  )

  return (
    <div className="content wide">
      <div className="hero">
        <h1>{project.name}</h1>
        <p>
          <span className={`badge ${project.status}`}>{project.status}</span> · {apps.length} application
          {apps.length === 1 ? '' : 's'} · {findings.length} finding{findings.length === 1 ? '' : 's'}
        </p>
      </div>

      {error && <div className="banner error">⚠ {error}</div>}

      <div className="tabs">
        {(['assets', 'methodology', 'knowledge', 'context', 'scope', 'scan', 'repeater', 'proxy', 'terminal', 'playbooks', 'tasks', 'findings', 'reports', 'graph', 'analyst', 'audit'] as Tab[]).map((t) => (
          <button key={t} className={`tab ${tab === t ? 'on' : ''}`} onClick={() => setTab(t)}>
            {t === 'assets' ? 'Applications & Assets' : t === 'scan' ? 'Scan' : t[0].toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {tab === 'assets' && <AssetsTab project={project} apps={apps} online={online} reload={loadApps} onError={setError} />}
      {tab === 'methodology' && <MethodologyTab project={project} online={online} onError={setError} />}
      {tab === 'knowledge' && <KnowledgeTab project={project} online={online} onError={setError} />}
      {tab === 'context' && <ContextTab project={project} items={context} online={online} reload={async () => setContext((await api.listContext(project.id)) ?? [])} onError={setError} />}
      {tab === 'scope' && <ScopeTab project={project} online={online} onError={setError} />}
      {tab === 'scan' && <ScanTab assets={allAssets} capabilities={capabilities} online={online} afterFinding={loadAll} onError={setError} />}
      {tab === 'repeater' && <RepeaterTab project={project} online={online} onError={setError} />}
      {tab === 'proxy' && <ProxyTab project={project} online={online} onError={setError} />}
      {tab === 'terminal' && (
        <Suspense fallback={<div className="empty">Loading terminal…</div>}>
          <TerminalTab project={project} online={online} onError={setError} />
        </Suspense>
      )}
      {tab === 'playbooks' && <PlaybooksTab assets={allAssets} online={online} onError={setError} />}
      {tab === 'tasks' && <TasksTab online={online} onError={setError} />}
      {tab === 'findings' && <FindingsTab findings={findings} />}
      {tab === 'reports' && <ReportsTab project={project} online={online} onError={setError} />}
      {tab === 'graph' && <GraphTab project={project} online={online} onError={setError} />}
      {tab === 'analyst' && <AnalystPanel project={project} online={online} />}
      {tab === 'audit' && <AuditTab online={online} onError={setError} />}
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
