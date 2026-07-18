import { useEffect, useMemo, useState, type FormEvent, type ChangeEvent } from 'react'
import {
  api,
  Application,
  Asset,
  CapabilityManifest,
  ContextItem,
  Finding,
  Observation,
  Playbook,
  PlaybookRunResult,
  Project,
  TaskOutcome,
} from './api'
import { AnalystPanel } from './AnalystPanel'
import { TasksTab } from './TasksTab'
import { hasNativePickers, pickDirectory } from './native'

type Tab = 'assets' | 'context' | 'scan' | 'playbooks' | 'tasks' | 'findings' | 'analyst'

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
        {(['assets', 'context', 'scan', 'playbooks', 'tasks', 'findings', 'analyst'] as Tab[]).map((t) => (
          <button key={t} className={`tab ${tab === t ? 'on' : ''}`} onClick={() => setTab(t)}>
            {t === 'assets' ? 'Applications & Assets' : t === 'scan' ? 'Scan' : t[0].toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {tab === 'assets' && <AssetsTab project={project} apps={apps} online={online} reload={loadApps} onError={setError} />}
      {tab === 'context' && <ContextTab project={project} items={context} online={online} reload={async () => setContext((await api.listContext(project.id)) ?? [])} onError={setError} />}
      {tab === 'scan' && <ScanTab assets={allAssets} capabilities={capabilities} online={online} afterFinding={loadAll} onError={setError} />}
      {tab === 'playbooks' && <PlaybooksTab assets={allAssets} online={online} onError={setError} />}
      {tab === 'tasks' && <TasksTab online={online} onError={setError} />}
      {tab === 'findings' && <FindingsTab findings={findings} />}
      {tab === 'analyst' && <AnalystPanel project={project} online={online} />}
    </div>
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
