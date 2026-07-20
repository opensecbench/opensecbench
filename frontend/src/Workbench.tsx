import { Component, lazy, Suspense, useEffect, useMemo, useRef, useState, type FormEvent, type ChangeEvent, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react'
import {
  api,
  Application,
  Artifact,
  Asset,
  CapabilityManifest,
  ContextItem,
  CoverageView,
  AuditEvent,
  Finding,
  HTTPExchange,
  Observation,
  Playbook,
  Report,
  ReportTemplate,
  PlaybookRun,
  Project,
  RunnerView,
  ScopeEntry,
  SearchResult,
  Task,
  TaskOutcome,
  TreeEntry,
} from './api'
import { AnalystPanel } from './AnalystPanel'
import { CodeView } from './CodeView'
import { LocationChip, OpenCode, parseLoc } from './CodeLink'
import { NotificationBell } from './NotificationBell'
import { GraphTab } from './GraphTab'
import { IntegrationsTab } from './IntegrationsTab'
import { InvestigationsTab } from './InvestigationsTab'
import { KnowledgeTab } from './KnowledgeTab'
import { InterceptTab } from './InterceptTab'
import { MethodologyTab } from './MethodologyTab'
import { OrchestrateTab } from './OrchestrateTab'
import { ProxyTab } from './ProxyTab'
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
  | 'replay'
  | 'proxy'
  | 'intercept'
  | 'terminal'
  | 'playbooks'
  | 'orchestrate'
  | 'tasks'
  | 'findings'
  | 'investigations'
  | 'reports'
  | 'graph'
  | 'integrations'
  | 'audit'
  | 'code'

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
  { key: 'investigations', icon: '🔎', label: 'Investg' },
  { key: 'replay', icon: '↔', label: 'Replay' },
  { key: 'proxy', icon: '📡', label: 'Proxy' },
  { key: 'intercept', icon: '✋', label: 'Intcpt' },
  { key: 'terminal', icon: '▤', label: 'Term' },
  { key: 'scan', icon: '▷', label: 'Scan' },
  { key: 'playbooks', icon: '🧩', label: 'Play' },
  { key: 'orchestrate', icon: '🤖', label: 'Agents' },
  { key: 'tasks', icon: '☰', label: 'Tasks' },
  { key: 'graph', icon: '📊', label: 'Graph' },
  { key: 'scope', icon: '🛡', label: 'Scope' },
  { key: 'reports', icon: '📄', label: 'Report', meta: true },
  { key: 'integrations', icon: '🔌', label: 'Integr', meta: true },
  { key: 'audit', icon: '📜', label: 'Audit', meta: true },
]

// Surfaces that can hold more than one open document at a time — only these get a
// document-tab row. Every other surface is a singleton reached solely via the
// activity bar, so it needs no tabs (ADR-0015: one way to switch, not two or three).
// `code` (source files, ADR-0050) has no activity-bar icon — you open specific files,
// you don't navigate to a blank Code surface — but many can be open at once.
const MULTI_DOC_SURFACES: Tab[] = ['replay', 'code']

function surfaceTitle(t: Tab): string {
  if (t === 'assets') return 'Applications & Assets'
  if (t === 'scan') return 'Scan'
  if (t === 'orchestrate') return 'Agent Playbooks'
  if (t === 'code') return 'Source'
  return t[0].toUpperCase() + t.slice(1)
}

// Icon for a document's tab. `code` documents aren't in SURFACES (no activity-bar icon), so give them a
// file glyph directly; everything else looks up its surface icon.
function docIcon(surface: Tab): string {
  if (surface === 'code') return '📄'
  return SURFACES.find((s) => s.key === surface)?.icon ?? '📄'
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
  if (!items?.length) return 0
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
  if (t === 'context') return 'Context'
  if (t === 'code') return 'Source'
  if (t === 'investigations') return 'Investigations'
  return 'Project'
}

// TreeNode is one row in a lazy source file tree: directories fetch their children on first expand, files
// open in CodeView on click (ADR-0050). Kept deliberately simple — a compact browser for the 220px Explorer.
function TreeNode({
  assetId,
  entry,
  depth,
  online,
  onOpenFile,
}: {
  assetId: string
  entry: TreeEntry
  depth: number
  online: boolean
  onOpenFile: (path: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [kids, setKids] = useState<TreeEntry[] | null>(null)
  async function toggle() {
    if (!entry.dir) {
      onOpenFile(entry.path)
      return
    }
    const next = !open
    setOpen(next)
    if (next && kids === null && online) {
      try {
        setKids((await api.assetTree(assetId, entry.path)) ?? [])
      } catch {
        setKids([])
      }
    }
  }
  return (
    <>
      <div className="wb-exp-row file" style={{ paddingLeft: 12 + depth * 12 }} onClick={toggle} title={entry.path}>
        <span className="ic">{entry.dir ? (open ? '▾' : '▸') : '·'}</span>
        <span className="lbl">{entry.name}</span>
      </div>
      {open && kids?.map((k) => (
        <TreeNode key={k.path} assetId={assetId} entry={k} depth={depth + 1} online={online} onOpenFile={onOpenFile} />
      ))}
    </>
  )
}

function FileTree({ assetId, online, onOpenFile }: { assetId: string; online: boolean; onOpenFile: (path: string) => void }) {
  const [roots, setRoots] = useState<TreeEntry[] | null>(null)
  useEffect(() => {
    if (!online) return
    let alive = true
    api.assetTree(assetId, '').then((r) => alive && setRoots(r ?? [])).catch(() => alive && setRoots([]))
    return () => { alive = false }
  }, [assetId, online])
  if (roots === null) return <div className="wb-exp-empty">Loading files…</div>
  if (roots.length === 0) return <div className="wb-exp-empty">No files on disk.</div>
  return <>{roots.map((e) => <TreeNode key={e.path} assetId={assetId} entry={e} depth={0} online={online} onOpenFile={onOpenFile} />)}</>
}

function WorkbenchExplorer({
  tab,
  project,
  apps,
  findings,
  observations,
  context,
  coverage,
  online,
  codeAssetId,
  onJump,
  onOpenCode,
}: {
  tab: Tab | null
  project: Project
  apps: AppAssets[]
  findings: Finding[]
  observations: Observation[]
  context: ContextItem[]
  coverage: CoverageView | null
  online: boolean
  codeAssetId: string | null
  onJump: (t: Tab) => void
  onOpenCode: OpenCode
}) {
  // Source-repo assets, and the observations that carry a source location — the inputs to the file browser
  // and the "findings in files" view.
  const sourceAssets = apps.flatMap((a) => a.assets.filter((as) => as.type === 'source_repo'))
  const located = observations.filter((o) => o.asset_id && o.location)
  return (
    <aside className="wb-explorer">
      <div className="wb-exp-head">
        {explorerTitle(tab)} <span className="r">{project.name}</span>
      </div>
      <div className="wb-exp-body">
        {tab === 'methodology' ? (
          (coverage?.packs ?? []).length === 0 ? (
            <div className="wb-exp-empty">No methodology adopted yet.</div>
          ) : (
            (coverage?.packs ?? []).map((p) => {
              const pct = packPct(p.items ?? [])
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
                {a.assets.map((as) =>
                  as.type === 'source_repo' ? (
                    <div key={as.id}>
                      <div className="wb-exp-row ind" title={as.location}>
                        <span className="ic">🗄</span>
                        <span className="lbl">{as.location.split('/').pop() || as.location}</span>
                      </div>
                      <FileTree assetId={as.id} online={online} onOpenFile={(path) => onOpenCode(as.id, path)} />
                    </div>
                  ) : (
                    <div key={as.id} className="wb-exp-row ind" title={as.location}>
                      <span className="ic">{ASSET_ICON[as.type] ?? '•'}</span>
                      <span className="lbl">{as.location}</span>
                    </div>
                  ),
                )}
              </div>
            ))
          )
        ) : tab === 'code' ? (
          codeAssetId ? (
            <FileTree assetId={codeAssetId} online={online} onOpenFile={(path) => onOpenCode(codeAssetId, path)} />
          ) : (
            <div className="wb-exp-empty">Open a file to browse its repo.</div>
          )
        ) : tab === 'findings' ? (
          findings.length === 0 && located.length === 0 ? (
            <div className="wb-exp-empty">No findings yet.</div>
          ) : (
            <>
              {SEVERITIES.map((sev) => {
                const n = findings.filter((f) => f.severity === sev).length
                if (!n) return null
                return (
                  <div key={sev} className="wb-exp-row">
                    <span className={`sev sev-${sev}`}>{sev}</span>
                    <span className="pct">{n}</span>
                  </div>
                )
              })}
              {located.length > 0 && (
                <>
                  <div className="wb-exp-row grp" style={{ marginTop: 8 }}>In files ({located.length})</div>
                  {located.map((o) => {
                    const { path, line } = parseLoc(o.location!)
                    return (
                      <div
                        key={o.id}
                        className="wb-exp-row file"
                        title={`${o.title} — ${o.location}`}
                        onClick={() => onOpenCode(o.asset_id!, path, line)}
                      >
                        <span className={`dot sev-${o.severity}`} />
                        <span className="lbl">{o.location}</span>
                      </div>
                    )
                  })}
                </>
              )}
            </>
          )
        ) : tab === 'context' ? (
          context.length === 0 ? (
            <div className="wb-exp-empty">No context ingested yet.</div>
          ) : (
            Object.entries(
              context.reduce<Record<string, ContextItem[]>>((acc, c) => {
                ;(acc[c.type] ??= []).push(c)
                return acc
              }, {}),
            ).map(([type, items]) => (
              <div key={type}>
                <div className="wb-exp-row grp">{type} ({items.length})</div>
                {items.map((c) => (
                  <div key={c.id} className="wb-exp-row ind" title={c.name}>
                    <span className="lbl">{c.name}</span>
                  </div>
                ))}
              </div>
            ))
          )
        ) : (
          <div className="wb-exp-project">
            <div className="wb-exp-fact"><span className={`badge ${project.status}`}>{project.status}</span></div>
            <div className="wb-exp-fact">{apps.length} app{apps.length === 1 ? '' : 's'} · {findings.length} finding{findings.length === 1 ? '' : 's'}</div>
            <div className="wb-exp-fact">{sourceAssets.length} source repo{sourceAssets.length === 1 ? '' : 's'}</div>
            {coverage && <div className="wb-exp-fact">Coverage {coverage.summary.covered_pct}%</div>}
            <div className="wb-exp-links">
              {(['methodology', 'assets', 'findings', 'replay'] as Tab[]).map((t) => (
                <button key={t} onClick={() => onJump(t)}>{SURFACES.find((s) => s.key === t)?.icon} {surfaceTitle(t)}</button>
              ))}
            </div>
          </div>
        )}
      </div>
    </aside>
  )
}

// A document is an open surface instance. Most are singletons keyed by their surface, but a
// Replay can be bound to a methodology test item (ADR-0015 P3b) — its own document whose saved
// evidence auto-attaches to that item.
interface Doc {
  key: string
  surface: Tab
  title: string
  bind?: { itemId: string; itemTitle: string }
  seed?: HTTPExchange // prefill a Replay from a captured request (Send to Replay)
  code?: { assetId: string; path: string; line?: number } // a source file opened in CodeView (ADR-0050)
}

function replayLabel(ex: HTTPExchange): string {
  try {
    return `Replay · ${ex.method} ${new URL(ex.url).pathname}`
  } catch {
    return `Replay · ${ex.method}`
  }
}

// Which surface a search hit lives on, so selecting it navigates there. Kinds
// come from the backend omni-search (store.Search); anything unmapped falls back
// to Assets so a click is never a dead end.
const HIT_SURFACE: Record<string, Tab> = {
  application: 'assets',
  asset: 'assets',
  finding: 'findings',
  observation: 'investigations',
  context: 'context',
  kb: 'knowledge',
}

// The titlebar omni-search: a real input over the already-shipped /v1/search
// (project-scoped). Results drop down live as you type; selecting one navigates
// to the surface that owns it. ⌘K / Ctrl+K focuses it from anywhere.
function OmniSearch({ online, onNavigate }: { online: boolean; onNavigate: (surface: Tab) => void }) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<SearchResult[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const boxRef = useRef<HTMLDivElement>(null)

  // Debounce so we hit the endpoint once the user pauses, not on every keystroke.
  useEffect(() => {
    const q = query.trim()
    if (!q) {
      setResults(null)
      setBusy(false)
      return
    }
    if (!online) return
    setBusy(true)
    const timer = setTimeout(async () => {
      try {
        const hits = (await api.search(q)) ?? []
        setResults(hits)
        setActive(0)
      } catch {
        setResults([])
      } finally {
        setBusy(false)
      }
    }, 220)
    return () => clearTimeout(timer)
  }, [query, online])

  // ⌘K / Ctrl+K focuses the search from anywhere in the Workbench.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
        e.preventDefault()
        inputRef.current?.focus()
        setOpen(true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Close the dropdown when focus leaves the whole widget.
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener('mousedown', onDown)
    return () => window.removeEventListener('mousedown', onDown)
  }, [])

  function choose(r: SearchResult) {
    onNavigate(HIT_SURFACE[r.kind] ?? 'assets')
    setOpen(false)
    setQuery('')
    setResults(null)
    inputRef.current?.blur()
  }

  function onInputKey(e: ReactKeyboardEvent<HTMLInputElement>) {
    if (!results || results.length === 0) {
      if (e.key === 'Escape') { setQuery(''); inputRef.current?.blur() }
      return
    }
    if (e.key === 'ArrowDown') { e.preventDefault(); setActive((i) => (i + 1) % results.length) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((i) => (i - 1 + results.length) % results.length) }
    else if (e.key === 'Enter') { e.preventDefault(); choose(results[active]) }
    else if (e.key === 'Escape') { setOpen(false); setQuery('') }
  }

  const showDrop = open && query.trim().length > 0
  return (
    <div className="wb-omni" ref={boxRef}>
      <span>⌕</span>
      <input
        ref={inputRef}
        value={query}
        onChange={(e) => { setQuery(e.target.value); setOpen(true) }}
        onFocus={() => setOpen(true)}
        onKeyDown={onInputKey}
        placeholder={online ? 'Search findings · assets · context · knowledge…' : 'Search unavailable — offline'}
        disabled={!online}
        spellCheck={false}
      />
      <kbd>⌘K</kbd>
      {showDrop && (
        <div className="wb-omni-drop">
          {busy && results === null && <div className="wb-omni-empty">Searching…</div>}
          {results !== null && results.length === 0 && !busy && <div className="wb-omni-empty">No matches.</div>}
          {results !== null && results.length > 0 && (
            <ul>
              {results.map((r, i) => (
                <li
                  key={r.kind + r.id}
                  className={i === active ? 'on' : ''}
                  onMouseEnter={() => setActive(i)}
                  onMouseDown={(e) => { e.preventDefault(); choose(r) }}
                >
                  <span className={`kind kind-${r.kind}`}>{r.kind}</span>
                  <span className="wb-omni-title">{r.title}</span>
                  {r.detail && <span className="muted">{r.detail}</span>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}

export function Workbench({ project, conn, initial, onHome }: { project: Project; conn: Conn; initial?: { surface?: string; thread?: string }; onHome: () => void }) {
  const online = conn === 'online'
  // Open documents are kept mounted so their state survives navigation (ADR-0015
  // Phase 3): switching surfaces hides the inactive ones, it never tears them down.
  const [openDocs, setOpenDocs] = useState<Doc[]>([
    { key: 'methodology', surface: 'methodology', title: surfaceTitle('methodology') }, // land on the coverage home
  ])
  const [activeKey, setActiveKey] = useState<string | null>('methodology')
  const [apps, setApps] = useState<AppAssets[]>([])
  const [capabilities, setCapabilities] = useState<CapabilityManifest[]>([])
  const [context, setContext] = useState<ContextItem[]>([])
  const [findings, setFindings] = useState<Finding[]>([])
  const [observations, setObservations] = useState<Observation[]>([])
  const [coverage, setCoverage] = useState<CoverageView | null>(null)
  const [methodReload, setMethodReload] = useState(0) // bump to make Methodology docs re-fetch
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
      setObservations((await api.listObservations(project.id)) ?? [])
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

  // Deep-link: a cockpit click can request a specific surface (e.g. tasks) on open.
  useEffect(() => {
    if (initial?.surface) openSurface(initial.surface as Tab)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial?.surface])

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

  // Evidence was attached to a methodology item from a bound Replay: refresh the status-bar/explorer
  // coverage and signal open Methodology documents to re-fetch so the item's evidence badge updates.
  async function afterEvidenceLinked() {
    setMethodReload((k) => k + 1)
    try {
      setCoverage(await api.getMethodologyCoverage(project.id))
    } catch {
      /* leave prior coverage; the surface reports its own errors */
    }
  }

  function focusOrAdd(doc: Doc) {
    setOpenDocs((docs) => (docs.some((d) => d.key === doc.key) ? docs : [...docs, doc]))
    setActiveKey(doc.key)
  }
  function openSurface(surface: Tab) {
    focusOrAdd({ key: surface, surface, title: surfaceTitle(surface) })
  }
  // The activity bar is the single navigator: activating a surface focuses its open
  // document, or opens one if none exists. Multi-document surfaces (Replay) focus
  // their most recent document rather than spawning a blank one — a blank one is
  // reached with the “+” in that surface's tab row.
  function activateSurface(surface: Tab) {
    const existing = openDocs.filter((d) => d.surface === surface)
    if (existing.length === 0) {
      openSurface(surface)
      return
    }
    const current = existing.find((d) => d.key === activeKey) ?? existing[existing.length - 1]
    setActiveKey(current.key)
  }
  // Open (or focus) a Replay bound to a methodology test item — the C payoff: evidence saved
  // here auto-attaches to that item.
  function openBoundReplay(itemId: string, itemTitle: string) {
    focusOrAdd({ key: `replay:${itemId}`, surface: 'replay', title: itemTitle, bind: { itemId, itemTitle } })
  }
  // Send to Replay: open a Replay document seeded with a captured request (ADR-0016 action).
  function openReplayFromExchange(ex: HTTPExchange) {
    focusOrAdd({ key: `replay:ex:${ex.id}`, surface: 'replay', title: replayLabel(ex), seed: ex })
  }
  // Open a source file in a CodeView document, scrolled to `line` (ADR-0050). One document per file; opening
  // the same file at a different line re-targets and re-scrolls the existing document rather than duplicating.
  function openCodeFile(assetId: string, path: string, line?: number) {
    const key = `code:${assetId}:${path}`
    const title = path.split('/').pop() ?? path
    setOpenDocs((docs) => {
      const existing = docs.find((d) => d.key === key)
      if (existing) {
        return docs.map((d) => (d.key === key ? { ...d, code: { assetId, path, line } } : d))
      }
      return [...docs, { key, surface: 'code' as Tab, title, code: { assetId, path, line } }]
    })
    setActiveKey(key)
  }
  function closeDoc(key: string, e?: ReactMouseEvent) {
    e?.stopPropagation()
    const idx = openDocs.findIndex((d) => d.key === key)
    const next = openDocs.filter((d) => d.key !== key)
    setOpenDocs(next)
    if (activeKey === key) setActiveKey(next[idx]?.key ?? next[idx - 1]?.key ?? null)
  }

  // Each open document renders its surface once and stays mounted; the frame only
  // toggles visibility. Adding a surface here makes it an openable document.
  function renderSurface(doc: Doc) {
    switch (doc.surface) {
      case 'assets':
        return <AssetsTab project={project} apps={apps} online={online} reload={loadApps} onError={setError} />
      case 'methodology':
        return <MethodologyTab project={project} online={online} onError={setError} onTestItem={openBoundReplay} reloadSignal={methodReload} />
      case 'knowledge':
        return <KnowledgeTab project={project} online={online} onError={setError} />
      case 'context':
        return <ContextTab project={project} items={context} online={online} reload={async () => setContext((await api.listContext(project.id)) ?? [])} onError={setError} />
      case 'scope':
        return <ScopeTab project={project} online={online} onError={setError} />
      case 'scan':
        return <ScanTab assets={allAssets} capabilities={capabilities} online={online} afterFinding={loadAll} onError={setError} />
      case 'replay':
        return <ReplayTab project={project} online={online} onError={setError} boundItem={doc.bind} seed={doc.seed} onEvidenceLinked={afterEvidenceLinked} />
      case 'proxy':
        return <ProxyTab project={project} online={online} onError={setError} onSendToReplay={openReplayFromExchange} />
      case 'intercept':
        return <InterceptTab project={project} online={online} onError={setError} />
      case 'terminal':
        return (
          <Suspense fallback={<div className="empty">Loading terminal…</div>}>
            <TerminalTab project={project} online={online} onError={setError} />
          </Suspense>
        )
      case 'playbooks':
        return <PlaybooksTab assets={allAssets} online={online} onError={setError} />
      case 'orchestrate':
        return <OrchestrateTab project={project} online={online} onError={setError} />
      case 'tasks':
        return <TasksTab online={online} onError={setError} />
      case 'findings':
        return <FindingsTab findings={findings} observations={observations} onOpenCode={openCodeFile} />
      case 'investigations':
        return <InvestigationsTab project={project} online={online} observations={observations} onOpenCode={openCodeFile} onError={setError} />
      case 'reports':
        return <ReportsTab project={project} online={online} onError={setError} />
      case 'graph':
        return <GraphTab project={project} online={online} onError={setError} />
      case 'integrations':
        return <IntegrationsTab project={project} online={online} onError={setError} />
      case 'audit':
        return <AuditTab online={online} onError={setError} />
      case 'code':
        return doc.code ? (
          <CodeView assetId={doc.code.assetId} path={doc.code.path} line={doc.code.line} online={online} />
        ) : null
    }
  }

  const activeSurface = openDocs.find((d) => d.key === activeKey)?.surface ?? null
  // Tabs only for surfaces that can hold several documents (Replay); everything
  // else is navigated from the activity bar alone. The row shows just the active
  // surface's documents, never a mix across surfaces.
  const showDocTabs = activeSurface !== null && MULTI_DOC_SURFACES.includes(activeSurface)
  const surfaceDocs = showDocTabs ? openDocs.filter((d) => d.surface === activeSurface) : []
  // When a source file is active, the Explorer browses that file's repo.
  const codeAssetId = openDocs.find((d) => d.key === activeKey)?.code?.assetId ?? null

  return (
    <div className="wb">
      <div className="wb-titlebar">
        <button className={`wb-proj ${online ? 'online' : ''}`} onClick={onHome} title="Back to Home">
          <span className="dot" /> {project.name} <span className="car">▾</span>
        </button>
        <OmniSearch online={online} onNavigate={activateSurface} />
        <NotificationBell online={online} />
        <code className="apiurl">{api.baseURL}</code>
      </div>

      <div className="wb-body">
        <nav className="wb-activity">
          {SURFACES.filter((s) => !s.meta).map((s) => (
            <button key={s.key} className={`wb-ic ${activeSurface === s.key ? 'on' : ''} ${openDocs.some((d) => d.surface === s.key) ? 'opened' : ''}`} title={surfaceTitle(s.key)} onClick={() => activateSurface(s.key)}>
              <span>{s.icon}</span>
              {s.key === 'findings' && findings.length > 0 && <span className="n red">{findings.length}</span>}
              {s.key === 'context' && context.length > 0 && <span className="n">{context.length}</span>}
              <small>{s.label}</small>
            </button>
          ))}
          <div className="wb-actsp" />
          <div className="wb-actdiv" />
          {SURFACES.filter((s) => s.meta).map((s) => (
            <button key={s.key} className={`wb-ic ${activeSurface === s.key ? 'on' : ''} ${openDocs.some((d) => d.surface === s.key) ? 'opened' : ''}`} title={surfaceTitle(s.key)} onClick={() => activateSurface(s.key)}>
              <span>{s.icon}</span>
              <small>{s.label}</small>
            </button>
          ))}
        </nav>

        <SurfaceBoundary>
          <WorkbenchExplorer
            tab={activeSurface}
            project={project}
            apps={apps}
            findings={findings}
            observations={observations}
            context={context}
            coverage={coverage}
            online={online}
            codeAssetId={codeAssetId}
            onJump={activateSurface}
            onOpenCode={openCodeFile}
          />
        </SurfaceBoundary>

        <div className="wb-center">
          {showDocTabs && (
            <div className="wb-doctabs">
              {surfaceDocs.map((d) => (
                <div key={d.key} className={`wb-doctab ${activeKey === d.key ? 'on' : ''}`} onClick={() => setActiveKey(d.key)} title={d.code?.path ?? d.title}>
                  <span className="em">{docIcon(d.surface)}</span>
                  <span className="lbl">{d.title}</span>
                  <span className="x" title="Close" onClick={(e) => closeDoc(d.key, e)}>✕</span>
                </div>
              ))}
              {activeSurface === 'replay' && (
                <button className="wb-doctab-new" title="New Replay" onClick={() => openSurface('replay')}>＋</button>
              )}
            </div>
          )}
          {error && <div className="banner error wb-banner">⚠ {error}</div>}
          <div className="wb-docarea">
            {openDocs.length === 0 && (
              <div className="empty">No document open — pick a surface from the activity bar on the left.</div>
            )}
            {openDocs.map((d) => (
              <div key={d.key} className="wb-doc" style={{ display: activeKey === d.key ? 'block' : 'none' }}>
                <SurfaceBoundary>{renderSurface(d)}</SurfaceBoundary>
              </div>
            ))}
          </div>
        </div>

        <SurfaceBoundary>
          <AnalystPanel project={project} online={online} initialThread={initial?.thread} />
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

function ReplayTab({
  project,
  online,
  onError,
  boundItem,
  seed,
  onEvidenceLinked,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
  boundItem?: { itemId: string; itemTitle: string }
  seed?: HTTPExchange
  onEvidenceLinked?: () => void
}) {
  const [history, setHistory] = useState<HTTPExchange[]>([])
  // Seed prefills the editor from a captured request (Send to Replay); the doc stays mounted so
  // this initial state is set once and then freely edited.
  const [method, setMethod] = useState(seed?.method ?? 'GET')
  const [url, setUrl] = useState(seed?.url ?? '')
  const [headers, setHeaders] = useState(seed?.request_headers ?? '')
  const [body, setBody] = useState(seed?.request_body ?? '')
  const [current, setCurrent] = useState<HTTPExchange | null>(null)
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [runners, setRunners] = useState<RunnerView[]>([])
  const [via, setVia] = useState('') // '' = local host; else a runner id (ADR-0025)

  async function reload() {
    setHistory((await api.listExchanges(project.id)) ?? [])
  }

  useEffect(() => {
    if (online) void reload().catch((e) => onError((e as Error).message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  // Enrolled runners offer alternate egress vantages for a send (ADR-0025).
  useEffect(() => {
    if (online) api.listRunners().then((r) => setRunners(r ?? [])).catch(() => {})
  }, [online])

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
      const sent = await api.sendExchange(ex.id, via || undefined)
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
      await api.saveExchangeEvidence(current.id, '', boundItem?.itemId)
      setSaved(true)
      if (boundItem) onEvidenceLinked?.()
    } catch (err) {
      onError((err as Error).message)
    }
  }

  return (
    <>
      {boundItem && (
        <div className="replay-context">
          <span className="pin">⛓ in context of</span> <b>{boundItem.itemTitle}</b>
          <span className="muted"> — evidence you save here attaches to this test item</span>
        </div>
      )}
      <section className="panel">
      <div className="panel-head">Replay</div>
      <p className="hint">
        Craft a request and send it. Targets are checked against the project scope allowlist before
        anything leaves the machine.
      </p>
      <div className="replay">
        <form className="replay-req" onSubmit={send}>
          <div className="replay-line">
            <select value={method} onChange={(e) => setMethod(e.target.value)}>
              {HTTP_METHODS.map((m) => (
                <option key={m} value={m}>{m}</option>
              ))}
            </select>
            <input
              className="replay-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://api.acme.com/v2/users"
              disabled={!online || busy}
            />
            {runners.some((r) => r.online) && (
              <select value={via} onChange={(e) => setVia(e.target.value)} title="Egress vantage" disabled={!online || busy}>
                <option value="">via: local host</option>
                {runners.filter((r) => r.online).map((r) => (
                  <option key={r.id} value={r.id}>via: {r.name}</option>
                ))}
              </select>
            )}
            <button type="submit" disabled={!online || busy || !url.trim()}>
              {busy ? 'Sending…' : 'Send'}
            </button>
          </div>
          <label className="replay-label">Headers</label>
          <textarea
            className="mono"
            rows={4}
            value={headers}
            onChange={(e) => setHeaders(e.target.value)}
            placeholder={'Authorization: Bearer …\nContent-Type: application/json'}
          />
          <label className="replay-label">Body</label>
          <textarea className="mono" rows={5} value={body} onChange={(e) => setBody(e.target.value)} />
        </form>

        <div className="replay-res">
          {current && current.sent_at ? (
            <>
              <div className="replay-status">
                <span className={`badge ${statusClass(current.status)}`}>{current.status ?? '—'}</span>
                {current.duration_ms != null && <span className="muted">{current.duration_ms} ms</span>}
                {current.egress && <span className="mc-pill" title="Egress vantage">via {runners.find((r) => r.id === current.egress)?.name ?? current.egress}</span>}
                <button className="link" onClick={saveEvidence} disabled={saved}>
                  {saved ? '✓ saved as evidence' : boundItem ? 'save as evidence → item' : 'save as evidence'}
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
        <ul className="rows replay-history">
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
    </>
  )
}

function statusClass(status?: number): string {
  if (status == null) return ''
  if (status >= 400) return 'failed'
  if (status >= 200 && status < 300) return 'succeeded'
  return 'active'
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
  const [phase, setPhase] = useState('') // 'pending' | 'running' while polling an async task
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
      // The task runs asynchronously on the worker pool (ADR-0022): enqueue, then poll to completion.
      const pending = await api.runTask({ capability_id: capId, asset_id: assetId, params, actor: 'human' })
      setPhase(pending.status)
      let task = pending
      for (let i = 0; i < 1800 && (task.status === 'pending' || task.status === 'running'); i++) {
        await new Promise((r) => setTimeout(r, 1000))
        task = await api.getTask(pending.id)
        setPhase(task.status)
      }
      // Assemble the outcome the panel expects from the finished task's artifacts + observations.
      const [artifacts, observations] = await Promise.all([
        api.getTaskArtifacts(task.id).catch(() => [] as Artifact[]),
        api.listTaskObservations(task.id).catch(() => [] as Observation[]),
      ])
      setOutcome({ task, artifacts, observations })
      const st: Record<string, string> = {}
      for (const o of observations) st[o.id] = o.review_state
      setObsState(st)
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setRunning(false)
      setPhase('')
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
          <button disabled={!online || running || !capId || !assetId} onClick={run}>{running ? (phase === 'pending' ? 'Queued…' : 'Running…') : '▷ Run'}</button>
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
                    <div className="obs-title">
                      {o.title}
                      {o.attributes?.verified === 'true' && <span className="mc-pill" title="Verified by the tool"> verified</span>}
                      {o.attributes?.reachable === 'true' && <span className="mc-pill" title="Reachable in the call graph"> reachable</span>}
                      {o.attributes?.reachable === 'false' && <span className="mc-pill" title="Imported but not called"> not reached</span>}
                      {o.attributes?.exposed === 'true' && <span className="mc-pill" title="On a network-exposed service"> exposed</span>}
                      {o.attributes?.exposed_route && <span className="mc-pill" title={o.attributes?.route_observed === 'true' ? 'In the handler file of a traffic-confirmed route' : 'In the handler file of a declared route'}>🌐 {o.attributes.exposed_route}</span>}
                    </div>
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
  const [pbRun, setPbRun] = useState<PlaybookRun | null>(null)
  const [steps, setSteps] = useState<Task[]>([])

  useEffect(() => {
    if (!online) return
    api.listPlaybooks().then((p) => setPlaybooks(p ?? [])).catch((e) => onError((e as Error).message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  async function run() {
    if (!pbId || !assetId) return
    setRunning(true)
    setPbRun(null)
    setSteps([])
    try {
      // The playbook runs asynchronously (ADR-0022): enqueue, then poll the run for live progress.
      let pr = await api.runPlaybook(pbId, assetId)
      setPbRun(pr)
      for (let i = 0; i < 3600 && pr.status === 'running'; i++) {
        await new Promise((r) => setTimeout(r, 1000))
        pr = await api.getPlaybookRun(pr.id)
        setPbRun(pr)
        // Resolve the tasks recorded so far to show each step's status as it completes.
        const tasks = await Promise.all((pr.task_ids ?? []).map((id) => api.getTask(id).catch(() => null)))
        setSteps(tasks.filter((t): t is Task => t !== null))
      }
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

      {pbRun && (
        <section className="panel">
          <div className="panel-head">
            Run <span className={`badge ${pbRun.status}`}>{pbRun.status}</span> · {steps.length}/{pbRun.task_ids?.length || 0} step(s)
          </div>
          {steps.length === 0 ? (
            <div className="empty">{pbRun.status === 'running' ? 'Starting…' : 'No steps ran.'}</div>
          ) : (
            <ul className="rows">
              {steps.map((t, i) => (
                <li key={t.id} className="row-item">
                  <span className="muted">#{i + 1}</span>
                  <span className={`badge ${t.status}`}>{t.status}</span>
                  <span className="row-title">{t.capability_id}</span>
                </li>
              ))}
            </ul>
          )}
          <div className="empty" style={{ textAlign: 'left' }}>
            Review each step's output and triage observations in the Tasks tab.
          </div>
        </section>
      )}
    </div>
  )
}

function FindingsTab({
  findings,
  observations,
  onOpenCode,
}: {
  findings: Finding[]
  observations: Observation[]
  onOpenCode: OpenCode
}) {
  // Index observations by id so each finding can show its supporting locations (findings carry no location
  // of their own — it lives on the observations they were promoted from, ADR-0050).
  const byId = useMemo(() => new Map(observations.map((o) => [o.id, o])), [observations])
  return (
    <section className="panel">
      <div className="panel-head">Findings ({findings.length})</div>
      {findings.length === 0 ? (
        <div className="empty">No findings yet. Run a scan and promote confirmed observations.</div>
      ) : (
        <ul className="rows">
          {findings.map((f) => {
            const obs = f.observation_ids.map((id) => byId.get(id)).filter((o): o is Observation => !!o)
            const located = obs.filter((o) => o.location)
            return (
              <li key={f.id} className="row-item col">
                <div className="row-main">
                  <span className={`sev sev-${f.severity}`}>{f.severity}</span>
                  <span className={`badge ${f.status}`}>{f.status}</span>
                  <span className="row-title">{f.title}</span>
                  {f.cwe && <span className="muted">{f.cwe}</span>}
                </div>
                {located.length > 0 && (
                  <div className="loc-row">
                    {located.map((o) => (
                      <LocationChip key={o.id} obs={o} onOpenCode={onOpenCode} />
                    ))}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
