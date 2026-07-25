import { Component, lazy, Suspense, useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type ChangeEvent, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react'
import {
  api,
  BEHAVIORAL_CONTEXT_TAGS,
  Application,
  Artifact,
  Asset,
  CapabilityManifest,
  ContextItem,
  CoverageView,
  AuditEvent,
  Engagement,
  Finding,
  HTTPExchange,
  Investigation,
  Observation,
  Playbook,
  Report,
  ReportTemplate,
  CodeHit,
  PlaybookRun,
  Project,
  RouteView,
  RunnerView,
  ScopeEntry,
  SearchResult,
  Task,
  TaskOutcome,
  TreeEntry,
} from './api'
import { AnalystPanel } from './AnalystPanel'
import { LocationChip, OpenCode } from './CodeLink'
import { DataTable, Column } from './DataTable'
import { EngagementSettings } from './EngagementSettings'
import { NotificationBell } from './NotificationBell'
import { ActivityMenu } from './ActivityMenu'
import { GraphTab } from './GraphTab'
import { RoutesTab } from './RoutesTab'
import { FindingReachability } from './FindingReachability'
import { AssetEcosystems } from './AssetEcosystems'
import { IntegrationsTab } from './IntegrationsTab'
import { InvestigationsTab } from './InvestigationsTab'
import { KnowledgeTab } from './KnowledgeTab'
import { InterceptTab } from './InterceptTab'
import { MethodologyTab } from './MethodologyTab'
import { OrchestrateTab } from './OrchestrateTab'
import { OverviewTab } from './Overview'
import { ProxyTab } from './ProxyTab'
import { ActivityTab } from './ActivityTab'
import { hasNativePickers, pickDirectory } from './native'

// The terminal pulls in xterm.js; load it only when the tab is opened.
const TerminalTab = lazy(() => import('./TerminalTab').then((m) => ({ default: m.TerminalTab })))
// CodeView pulls in highlight.js + its language grammars; load it only when a source file is opened.
const CodeView = lazy(() => import('./CodeView').then((m) => ({ default: m.CodeView })))

type Tab =
  | 'overview'
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
  | 'observations'
  | 'findings'
  | 'investigations'
  | 'routes'
  | 'reports'
  | 'graph'
  | 'integrations'
  | 'audit'
  | 'settings'
  | 'code'
  | 'finding'

type Conn = 'connecting' | 'online' | 'offline'

interface AppAssets {
  app: Application
  assets: Asset[]
}

// The activity bar surfaces (ADR-0015). The Analyst is not here — it is the
// right-hand dock, always present, never a surface you navigate to.
//
// `explorer: true` marks a surface whose left Explorer panel is a genuine second axis — a
// navigable structure that drives a *different* center view (a file tree feeding the code
// viewer, a route selector feeding the route detail). Surfaces without it render no Explorer
// at all and the document center reclaims the width: a panel that only mirrored the center's
// own list, or sat empty, was worse than none. `code` (ADR-0050) is not in this array — it has
// no activity-bar icon — but it too has an Explorer; see surfaceHasExplorer.
const SURFACES: { key: Tab; icon: string; label: string; meta?: boolean; explorer?: boolean }[] = [
  { key: 'overview', icon: '◆', label: 'Overview' },
  { key: 'assets', icon: '🗂', label: 'Assets', explorer: true },
  { key: 'context', icon: '🔬', label: 'Context', explorer: true },
  { key: 'knowledge', icon: '📚', label: 'Know' },
  { key: 'replay', icon: '↔', label: 'Replay' },
  { key: 'proxy', icon: '📡', label: 'Proxy' },
  { key: 'intercept', icon: '✋', label: 'Intcpt' },
  { key: 'terminal', icon: '▤', label: 'Term' },
  { key: 'scan', icon: '▷', label: 'Scan' },
  { key: 'observations', icon: '🧪', label: 'Observe' },
  { key: 'findings', icon: '⚑', label: 'Find', explorer: true },
  { key: 'investigations', icon: '🔎', label: 'Invest' },
  { key: 'routes', icon: '🎯', label: 'Surface', explorer: true },
  { key: 'graph', icon: '📊', label: 'Graph' },
  { key: 'methodology', icon: '✓', label: 'Method', explorer: true },
  { key: 'scope', icon: '🛡', label: 'Scope' },
  { key: 'orchestrate', icon: '🤖', label: 'Agents' },
  { key: 'playbooks', icon: '🧩', label: 'Play' },
  { key: 'tasks', icon: '🕘', label: 'Activity' },
  { key: 'reports', icon: '📄', label: 'Report' },
  { key: 'integrations', icon: '🔌', label: 'Integr', meta: true },
  { key: 'settings', icon: '⚙', label: 'Settings', meta: true },
  { key: 'audit', icon: '📜', label: 'Audit', meta: true },
]

// The activity bar's primary surfaces, grouped by what they're for (ADR-0015 declutter). The rail is
// ordered by how often each group is reached day-to-day (evidence → analysis → testing → run → coverage
// → deliver): the evidence you set up and the triage you live in sit up top, the testing tools drop to
// mid-list, and end-of-engagement coverage/deliver sit last. Meta surfaces (Settings/Integrations/Audit)
// still sit below a divider; global config/library moves out of the project entirely in a later step.
const SURFACE_GROUPS: { label: string; keys: Tab[] }[] = [
  { label: 'Evidence', keys: ['assets', 'context', 'knowledge'] },
  { label: 'Analysis', keys: ['observations', 'findings', 'investigations', 'routes', 'graph'] },
  { label: 'Testing', keys: ['replay', 'proxy', 'intercept', 'terminal', 'scan'] },
  { label: 'Run', keys: ['orchestrate', 'playbooks', 'tasks'] },
  { label: 'Coverage', keys: ['methodology', 'scope'] },
  { label: 'Deliver', keys: ['reports'] },
]
const surfaceByKey = (k: Tab) => SURFACES.find((s) => s.key === k)!

// Surfaces that can hold more than one open document at a time — only these get a
// document-tab row. Every other surface is a singleton reached solely via the
// activity bar, so it needs no tabs (ADR-0015: one way to switch, not two or three).
// `code` (source files, ADR-0050) has no activity-bar icon — you open specific files,
// you don't navigate to a blank Code surface — but many can be open at once.
const MULTI_DOC_SURFACES: Tab[] = ['replay', 'code', 'finding', 'context']

function surfaceTitle(t: Tab): string {
  if (t === 'overview') return 'Overview'
  if (t === 'assets') return 'Applications & Assets'
  if (t === 'scan') return 'Scan'
  if (t === 'orchestrate') return 'Agent Playbooks'
  if (t === 'settings') return 'Engagement settings'
  if (t === 'code') return 'Source'
  if (t === 'finding') return 'Finding'
  return t[0].toUpperCase() + t.slice(1)
}

// Icon for a document's tab. `code` documents aren't in SURFACES (no activity-bar icon), so give them a
// file glyph directly; everything else looks up its surface icon.
function docIcon(surface: Tab): string {
  if (surface === 'code') return '📄'
  if (surface === 'finding') return '⚑'
  return SURFACES.find((s) => s.key === surface)?.icon ?? '📄'
}

// authWarning returns a soft-gate message when an engagement lacks recorded authorization or it has expired
// (ADR-0051). Soft: it warns (banner + pre-run confirm), it does not block — an engagement shouldn't trap
// mid-flight work, and authorization is a record, not cryptographic proof.
function authWarning(e: Engagement | null): string | null {
  if (!e) return null
  if (!e.authorized) return 'No written authorization is recorded for this engagement.'
  if (e.auth_to && new Date(e.auth_to) < new Date(new Date().toDateString())) return `Authorization expired on ${e.auth_to}.`
  return null
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
  if (t === 'routes') return 'Routes'
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

// RoutesExplorer is the routes surface's selector (ADR-0033, workbench split): search and filter the ranked
// attack-surface list here; the chosen route is inspected in the center. The list is the browse axis, the
// center is the detail — deliberately not the same list twice. Filter/search state is local to the panel.
function RoutesExplorer({
  routes,
  selectedId,
  onSelect,
}: {
  routes: RouteView[]
  selectedId: string | null
  onSelect: (id: string) => void
}) {
  const [q, setQ] = useState('')
  const [observedOnly, setObservedOnly] = useState(false)
  const [withFindings, setWithFindings] = useState(false)
  const needle = q.trim().toLowerCase()
  const shown = routes.filter(
    (r) =>
      (!observedOnly || r.observed) &&
      (!withFindings || (r.findings?.length ?? 0) > 0) &&
      (!needle || r.path.toLowerCase().includes(needle) || r.method.toLowerCase().includes(needle)),
  )
  const live = routes.filter((r) => r.observed).length
  const risky = routes.filter((r) => r.reachable_count > 0).length
  return (
    <div className="wb-routes-exp">
      <input className="wb-exp-search" placeholder="🔍 search routes…" value={q} onChange={(e) => setQ(e.target.value)} />
      <div className="wb-exp-chips">
        <button className={`chip ${observedOnly ? 'on' : ''}`} onClick={() => setObservedOnly((v) => !v)}>✔ live</button>
        <button className={`chip ${withFindings ? 'on' : ''}`} onClick={() => setWithFindings((v) => !v)}>⚑ findings</button>
      </div>
      <div className="wb-routes-list">
        {routes.length === 0 ? (
          <div className="wb-exp-empty">No routes yet — run route-map.</div>
        ) : shown.length === 0 ? (
          <div className="wb-exp-empty">No routes match.</div>
        ) : (
          shown.map((r) => {
            const n = r.findings?.length ?? 0
            return (
              <div
                key={r.id}
                className={`wb-route-row ${selectedId === r.id ? 'sel' : ''} ${r.reachable_count > 0 ? 'risk' : ''}`}
                onClick={() => onSelect(r.id)}
                title={`${r.method || 'ANY'} ${r.path}`}
              >
                <span className={`route-method m-${(r.method || 'any').toLowerCase()}`}>{r.method || 'ANY'}</span>
                <span className="wb-route-path mono">{r.path}</span>
                <span className="grow" />
                {r.observed && <span className="wb-route-live" title="Traffic-confirmed">✔</span>}
                {r.reachable_count > 0 ? (
                  <span className="wb-route-pip risk" title={`${r.reachable_count} reachable finding(s)`}>{r.reachable_count}</span>
                ) : n > 0 ? (
                  <span className="wb-route-pip" title={`${n} finding(s)`}>{n}</span>
                ) : null}
              </div>
            )
          })
        )}
      </div>
      <div className="wb-routes-foot">{routes.length} routes · {live} live · {risky} exploitable</div>
    </div>
  )
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
  routes,
  selectedRouteId,
  onSelectRoute,
  onOpenCode,
  onOpenFinding,
  onOpenContextItem,
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
  routes: RouteView[]
  selectedRouteId: string | null
  onSelectRoute: (id: string) => void
  onOpenCode: OpenCode
  onOpenFinding: (f: Finding) => void
  onOpenContextItem: (c: ContextItem) => void
}) {
  // "Findings in files": each finding's located observations — clicking opens the finding's detail. Raw
  // (un-promoted) observations do NOT appear here — they live on the Observations surface, their single home.
  const obsById = new Map(observations.map((o) => [o.id, o]))
  const findingLocs = findings.flatMap((f) =>
    f.observation_ids
      .map((id) => obsById.get(id))
      .filter((o): o is Observation => !!o && !!o.asset_id && !!o.location)
      .map((o) => ({ finding: f, obs: o })),
  )
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
          findings.length === 0 ? (
            <div className="wb-exp-empty">No findings yet. Scanner results land in the Observations tab — promote the real ones there.</div>
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
              {findingLocs.length > 0 && (
                <>
                  <div className="wb-exp-row grp" style={{ marginTop: 8 }}>Findings in files ({findingLocs.length})</div>
                  {findingLocs.map(({ finding, obs }) => (
                    <div
                      key={finding.id + ':' + obs.id}
                      className="wb-exp-row file"
                      title={`${finding.title} — ${obs.location} · open finding details`}
                      onClick={() => onOpenFinding(finding)}
                    >
                      <span className={`dot sev-${finding.severity}`} />
                      <span className="lbl">{obs.location}</span>
                    </div>
                  ))}
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
                  <div
                    key={c.id}
                    className="wb-exp-row ind ctx-open"
                    onClick={() => onOpenContextItem(c)}
                    title={(c.tags ?? []).length ? `${c.name} · ${(c.tags ?? []).join(', ')} · open` : `${c.name} · open`}
                  >
                    {c.pinned && <span className="ic">📌</span>}
                    <span className="lbl">{c.name}</span>
                  </div>
                ))}
              </div>
            ))
          )
        ) : tab === 'routes' ? (
          <RoutesExplorer routes={routes} selectedId={selectedRouteId} onSelect={onSelectRoute} />
        ) : null}
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
  finding?: { id: string } // a finding opened in its detail view; the row data is looked up live by id
  ctx?: { id: string } // a context item opened in its detail/editor view; looked up live by id
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
type OmniResult = SearchResult & { code?: CodeHit }

function OmniSearch({ online, projectId, onNavigate, onOpenCode }: { online: boolean; projectId: string; onNavigate: (surface: Tab, id?: string) => void; onOpenCode: (assetId: string, path: string, line?: number) => void }) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<OmniResult[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const boxRef = useRef<HTMLDivElement>(null)

  // Debounce so we hit the endpoints once the user pauses, not on every keystroke. Metadata (names,
  // findings, knowledge) and source-content (grep of the repos) run in parallel; code hits are the heavier
  // tier so they only run for queries of 3+ chars.
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
        const [meta, code] = await Promise.all([
          api.search(q).catch(() => [] as SearchResult[]),
          q.length >= 3 ? api.searchCode(projectId, q).catch(() => [] as CodeHit[]) : Promise.resolve([] as CodeHit[]),
        ])
        const codeResults: OmniResult[] = (code ?? []).map((c) => ({
          kind: 'code',
          id: `${c.asset_id}:${c.path}:${c.line}`,
          title: `${c.path}:${c.line}`,
          detail: c.text,
          code: c,
        }))
        setResults([...(meta ?? []), ...codeResults])
        setActive(0)
      } catch {
        setResults([])
      } finally {
        setBusy(false)
      }
    }, 260)
    return () => clearTimeout(timer)
  }, [query, online, projectId])

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

  function choose(r: OmniResult) {
    if (r.code) onOpenCode(r.code.asset_id, r.code.path, r.code.line)
    else onNavigate(HIT_SURFACE[r.kind] ?? 'assets', r.id)
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
        placeholder={online ? 'Search findings · code · assets · knowledge…' : 'Search unavailable — offline'}
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
    { key: 'overview', surface: 'overview', title: surfaceTitle('overview') }, // land on the start page
  ])
  const [activeKey, setActiveKey] = useState<string | null>('overview')
  const docAreaRef = useRef<HTMLDivElement>(null)
  const [apps, setApps] = useState<AppAssets[]>([])
  const [capabilities, setCapabilities] = useState<CapabilityManifest[]>([])
  const [context, setContext] = useState<ContextItem[]>([])
  const [findings, setFindings] = useState<Finding[]>([])
  const [observations, setObservations] = useState<Observation[]>([])
  const [engagement, setEngagement] = useState<Engagement | null>(null) // the engagement record (ADR-0051)
  const engTechniques = engagement?.techniques ?? null
  const [requireAuth, setRequireAuth] = useState(true) // global setting: warn when authorization isn't on file
  // Deep-link target: a surface + row id to scroll to and flash, with a nonce so repeats re-fire.
  const [focus, setFocus] = useState<{ surface: Tab; id: string; n: number } | null>(null)
  const [coverage, setCoverage] = useState<CoverageView | null>(null)
  const [routes, setRoutes] = useState<RouteView[]>([]) // attack-surface inventory, shared by the routes Explorer + detail
  const [selectedRouteId, setSelectedRouteId] = useState<string | null>(null)
  const [methodReload, setMethodReload] = useState(0) // bump to make Methodology docs re-fetch
  const [approvals, setApprovals] = useState(0)
  const [error, setError] = useState<string | null>(null)

  // Generic scroll-stuck fix for the mounted-and-toggled document frame. Each doc stays mounted with its
  // display toggled; a hidden doc keeps its scrollTop, and when it is re-shown — or its content shrank
  // while hidden — the browser does not re-clamp, leaving you stranded past the end with no scrollbar. On
  // every activation, re-clamp the active doc and any scrolled descendant to a valid offset. This only
  // touches containers actually scrolled past their content, so a still-valid remembered position is kept.
  useLayoutEffect(() => {
    const doc = docAreaRef.current?.querySelector<HTMLElement>(`[data-dockey="${CSS.escape(activeKey ?? '')}"]`)
    if (!doc) return
    const reclamp = (el: HTMLElement) => {
      if (el.scrollTop <= 0) return // fast path: not scrolled, nothing to clamp
      const max = el.scrollHeight - el.clientHeight
      if (el.scrollTop > max) el.scrollTop = Math.max(0, max)
    }
    reclamp(doc)
    doc.querySelectorAll<HTMLElement>('*').forEach(reclamp)
  }, [activeKey])

  async function loadApps() {
    const list = (await api.listApplications(project.id)) ?? []
    const withAssets = await Promise.all(
      list.map(async (app) => ({ app, assets: (await api.listAssets(app.id)) ?? [] })),
    )
    setApps(withAssets)
  }

  // Routes arrive already ranked by the risk behind each entry point (route→sink). Keep the current
  // selection if it survives the reload; otherwise fall to the top-ranked route so the detail pane is
  // never blank on open. Exposed as its own fn so the routes detail's Refresh can re-pull.
  async function loadRoutes() {
    const rs = (await api.projectRoutes(project.id)) ?? []
    setRoutes(rs)
    setSelectedRouteId((cur) => (cur && rs.some((r) => r.id === cur) ? cur : rs[0]?.id ?? null))
  }

  async function loadAll() {
    try {
      await loadApps()
      setCapabilities((await api.listCapabilities()) ?? [])
      setContext((await api.listContext(project.id)) ?? [])
      setFindings((await api.listFindings()) ?? [])
      setObservations((await api.listObservations(project.id)) ?? [])
      setEngagement((await api.getEngagement(project.id)) ?? null)
      setCoverage(await api.getMethodologyCoverage(project.id))
      await loadRoutes()
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  useEffect(() => {
    if (online) void loadAll()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  // The authorization soft-gate is opt-out via a global setting (default on).
  useEffect(() => {
    if (!online) return
    api.getSettings().then((s) => setRequireAuth(s.values['engagement.require_authorization'] !== 'false')).catch(() => {})
  }, [online])

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
  // Navigate to a surface and ask it to scroll/highlight a specific row (deep-link from omni-search). The
  // nonce makes re-selecting the same id re-fire the scroll even when the id is unchanged.
  function navigateTo(surface: Tab, id?: string) {
    activateSurface(surface)
    if (id) setFocus((f) => ({ surface, id, n: (f?.n ?? 0) + 1 }))
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
  // Open a finding's detail document (its info page): title, description, supporting observations and their
  // locations, and reachability. Clicking a finding row lands here rather than jumping straight to a file —
  // the source jumps are the explicit ↦ location chips within the detail. One document per finding.
  function openFinding(f: Finding) {
    const key = `finding:${f.id}`
    focusOrAdd({ key, surface: 'finding', title: f.title, finding: { id: f.id } })
  }
  // Open a context item (note/file) in an in-app detail+editor document. A separate document per item, kept
  // under the Context surface so the left list stays visible; the row data is looked up live by id.
  function openContextItem(ci: ContextItem) {
    focusOrAdd({ key: `ctx:${ci.id}`, surface: 'context', title: ci.name, ctx: { id: ci.id } })
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
      case 'overview':
        return (
          <OverviewTab
            project={project}
            assets={allAssets.length}
            findings={findings}
            coverage={coverage}
            engagement={engagement}
            online={online}
            onJump={(t) => activateSurface(t as Tab)}
            onScan={async () => {
              const res = await api.scanProject(project.id)
              void loadAll()
              // Jump to Tasks so you can watch the scans run and findings land.
              if ((res.enqueued?.length ?? 0) > 0) activateSurface('tasks')
              return res
            }}
          />
        )
      case 'assets':
        return <AssetsTab project={project} apps={apps} online={online} reload={loadApps} onError={setError} />
      case 'methodology':
        return <MethodologyTab project={project} online={online} onError={setError} onTestItem={openBoundReplay} reloadSignal={methodReload} />
      case 'knowledge':
        return <KnowledgeTab project={project} online={online} onError={setError} />
      case 'context':
        return doc.ctx ? (
          <ContextView
            item={context.find((c) => c.id === doc.ctx!.id) ?? null}
            online={online}
            reload={async () => setContext((await api.listContext(project.id)) ?? [])}
            onError={setError}
            onClose={() => closeDoc(doc.key)}
          />
        ) : (
          <ContextTab project={project} items={context} online={online} reload={async () => setContext((await api.listContext(project.id)) ?? [])} onError={setError} />
        )
      case 'scope':
        return <ScopeTab project={project} online={online} onError={setError} />
      case 'scan':
        return <ScanTab assets={allAssets} capabilities={capabilities} techniques={engTechniques} authWarn={authWarn} online={online} afterFinding={loadAll} onError={setError} />
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
        return <ActivityTab online={online} onError={setError} />
      case 'findings':
        return (
          <FindingsTab
            findings={findings}
            observations={observations}
            onOpenFinding={openFinding}
            onJump={(t) => activateSurface(t)}
            reload={loadAll}
            onError={setError}
            focusId={focus?.surface === 'findings' ? focus.id : undefined}
            focusNonce={focus?.n ?? 0}
          />
        )
      case 'observations':
        return (
          <ObservationsTab
            projectId={project.id}
            observations={observations}
            online={online}
            onOpenCode={openCodeFile}
            onJump={(t) => activateSurface(t as Tab)}
            reload={loadAll}
            onError={setError}
          />
        )
      case 'investigations':
        return <InvestigationsTab project={project} online={online} observations={observations} onOpenCode={openCodeFile} onJump={(t) => activateSurface(t as Tab)} onError={setError} />
      case 'reports':
        return <ReportsTab project={project} online={online} onError={setError} />
      case 'routes':
        return (
          <RoutesTab
            routes={routes}
            selectedRouteId={selectedRouteId}
            observations={observations}
            online={online}
            onReload={() => void loadRoutes().catch((e) => setError((e as Error).message))}
            onOpenCode={openCodeFile}
            onJump={(t) => activateSurface(t as Tab)}
          />
        )
      case 'graph':
        return <GraphTab project={project} online={online} onError={setError} />
      case 'integrations':
        return <IntegrationsTab project={project} online={online} onError={setError} />
      case 'audit':
        return <AuditTab online={online} onError={setError} />
      case 'settings':
        return <EngagementSettings project={project} online={online} onError={setError} onSaved={loadAll} />
      case 'code':
        return doc.code ? (
          <Suspense fallback={<div className="empty">Loading viewer…</div>}>
            <CodeView assetId={doc.code.assetId} path={doc.code.path} line={doc.code.line} online={online} />
          </Suspense>
        ) : null
      case 'finding':
        return doc.finding ? (
          <FindingDetail
            projectId={project.id}
            finding={findings.find((f) => f.id === doc.finding!.id) ?? null}
            observations={observations}
            online={online}
            onOpenCode={openCodeFile}
            reload={loadAll}
            onError={setError}
          />
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
  // The Explorer renders only for surfaces where it's a genuine second axis (declared via SURFACES.explorer,
  // plus `code` which has no activity-bar entry). Elsewhere it's suppressed and the center reclaims the width —
  // an empty or nav-duplicating panel was worse than none.
  const surfaceHasExplorer = activeSurface === 'code' || !!SURFACES.find((s) => s.key === activeSurface)?.explorer
  // Authorization soft-gate message, suppressed when the global setting turns the requirement off (ADR-0051).
  const authWarn = requireAuth ? authWarning(engagement) : null

  return (
    <div className="wb">
      <div className="wb-titlebar">
        <button className={`wb-proj ${online ? 'online' : ''}`} onClick={onHome} title="Back to Home">
          <span className="dot" /> {project.name} <span className="car">▾</span>
        </button>
        <OmniSearch online={online} projectId={project.id} onNavigate={navigateTo} onOpenCode={openCodeFile} />
        <ActivityMenu online={online} onOpen={(kind) => activateSurface(kind === 'plan' ? 'orchestrate' : 'tasks')} />
        <NotificationBell online={online} />
        <code className="apiurl">{api.baseURL}</code>
      </div>

      <div className="wb-body">
        <nav className="wb-activity">
          <button className={`wb-ic wb-ic-top ${activeSurface === 'overview' ? 'on' : ''}`} title="Overview — where the assessment stands" onClick={() => activateSurface('overview')}>
            <span>◆</span>
            <small>Overview</small>
          </button>
          {SURFACE_GROUPS.map((g) => (
            <div key={g.label} className="wb-actgrp">
              <div className="wb-actgrp-h">{g.label}</div>
              {g.keys.map((k) => surfaceByKey(k)).map((s) => (
                <button key={s.key} className={`wb-ic ${activeSurface === s.key ? 'on' : ''} ${openDocs.some((d) => d.surface === s.key) ? 'opened' : ''}`} title={surfaceTitle(s.key)} onClick={() => activateSurface(s.key)}>
                  <span>{s.icon}</span>
                  {s.key === 'findings' && findings.length > 0 && <span className="n red">{findings.length}</span>}
                  {s.key === 'context' && context.length > 0 && <span className="n">{context.length}</span>}
                  <small>{s.label}</small>
                </button>
              ))}
            </div>
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

        {surfaceHasExplorer && (
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
              routes={routes}
              selectedRouteId={selectedRouteId}
              onSelectRoute={setSelectedRouteId}
              onOpenCode={openCodeFile}
              onOpenFinding={openFinding}
              onOpenContextItem={openContextItem}
            />
          </SurfaceBoundary>
        )}

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
          {authWarn && (
            <div className="banner warn wb-banner">
              ⚠ {authWarn} <button className="link" onClick={() => activateSurface('settings')}>Record authorization →</button>
            </div>
          )}
          {error && <div className="banner error wb-banner">⚠ {error}</div>}
          <div className="wb-docarea" ref={docAreaRef}>
            {openDocs.length === 0 && (
              <div className="empty">No document open — pick a surface from the activity bar on the left.</div>
            )}
            {openDocs.map((d) => (
              <div key={d.key} data-dockey={d.key} className="wb-doc" style={{ display: activeKey === d.key ? 'block' : 'none' }}>
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
  const [narrate, setNarrate] = useState(true) // author an executive summary + per-finding impact/remediation (ADR-0045)
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
      const rep = await api.generateReport(project.id, template, format, narrate)
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
        <label className="rpt-narrate" title="The analyst drafts an executive summary + per-finding impact/remediation, grounded in the findings.">
          <input type="checkbox" checked={narrate} onChange={(e) => setNarrate(e.target.checked)} /> AI narrative
        </label>
        <button onClick={generate} disabled={!online || busy}>
          {busy ? (narrate ? 'Writing…' : 'Generating…') : 'Generate'}
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
              <span className="muted">{rep.template_id}</span>
              <span className="grow" />
              <span className="muted mono">{new Date(rep.created_at).toLocaleString()}</span>
              <a className="link" href={api.artifactContentURL(rep.artifact_id)} target="_blank" rel="noreferrer">open</a>
              <button
                className="del"
                title="Delete this report"
                disabled={!online}
                onClick={async () => {
                  if (!window.confirm(`Delete report "${rep.title}"?`)) return
                  try {
                    await api.deleteReport(rep.id)
                    await reload()
                  } catch (e) {
                    onError((e as Error).message)
                  }
                }}
              >
                ✕
              </button>
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

  // Editing an existing asset's sensitivity auto-saves on change — it gates external AI egress, so
  // operators need to correct it without deleting and re-adding the asset.
  async function changeAssetSensitivity(assetId: string, sensitivity: string) {
    try {
      await api.updateAssetSensitivity(assetId, sensitivity)
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
                  <li key={as.id} className="row-item col">
                    <div className="row-main">
                      <span className="badge">{as.type}</span>
                      <select
                        className={`sens sens-${as.sensitivity}`}
                        value={as.sensitivity}
                        disabled={!online}
                        title="Data sensitivity — gates whether this asset may reach an external AI provider (open_source ≤ internal ≤ private)"
                        onChange={(e) => changeAssetSensitivity(as.id, e.target.value)}
                      >
                        <option value="open_source">open_source</option>
                        <option value="internal">internal</option>
                        <option value="private">private</option>
                      </select>
                      <span className="mono">{as.location}</span>
                    </div>
                    {as.type === 'source_repo' && <AssetEcosystems assetId={as.id} online={online} onError={onError} />}
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
                <option value="open_source">open_source</option>
                <option value="internal">internal</option>
                <option value="private">private</option>
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
  const [busy, setBusy] = useState(false)
  const [noteTitle, setNoteTitle] = useState('')
  const [noteBody, setNoteBody] = useState('')
  const [tagsText, setTagsText] = useState('')
  const [pinned, setPinned] = useState(false)

  const tags = tagsText.split(',').map((t) => t.trim().toLowerCase()).filter(Boolean)
  // Suggest the reserved behavioral tags plus any already used in this project, minus what's already entered.
  const usedTags = Array.from(new Set(items.flatMap((i) => i.tags ?? [])))
  const suggestions = Array.from(new Set([...BEHAVIORAL_CONTEXT_TAGS, ...usedTags])).filter((t) => !tags.includes(t))
  const addTag = (t: string) => setTagsText([...tags, t].join(', '))
  const resetLabels = () => { setTagsText(''); setPinned(false) }

  async function addNote() {
    const body = noteBody.trim()
    if (!body) return
    const name = noteTitle.trim() || body.split('\n')[0].slice(0, 60)
    setBusy(true)
    try {
      await api.createNote(project.id, name, body, { tags, pinned })
      setNoteTitle(''); setNoteBody(''); resetLabels()
      await reload()
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function upload(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    setBusy(true)
    try {
      // Files are ingested as "document"; kind/category is expressed with tags, not a separate type picker.
      await api.ingestContext(project.id, file.name, 'document', file, { tags, pinned })
      e.target.value = ''
      resetLabels()
      await reload()
    } catch (err) {
      onError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">Add context</div>
      <p className="hint">Notes and files you add appear in the left panel, grouped by kind. Agents can read them; pin or tag one to fold it into every run.</p>

      <div className="ctx-compose">
        <input className="ctx-note-title" placeholder="Note title (optional)" value={noteTitle} onChange={(e) => setNoteTitle(e.target.value)} disabled={busy} />
        <textarea className="ctx-note-body" placeholder="Write a note… (findings context, constraints, hypotheses — agents can read these)" value={noteBody} onChange={(e) => setNoteBody(e.target.value)} disabled={busy} rows={3} />

        {/* Shared labels — applied to the note or file you add next. */}
        <input className="ctx-tags" placeholder="tags, comma-separated" value={tagsText} onChange={(e) => setTagsText(e.target.value)} disabled={busy} />
        {suggestions.length > 0 && (
          <div className="ctx-tag-suggest">
            {suggestions.map((t) => (
              <button key={t} type="button" className={`ctx-tag-chip ${BEHAVIORAL_CONTEXT_TAGS.includes(t) ? 'behavioral' : ''}`} onClick={() => addTag(t)} disabled={busy} title={BEHAVIORAL_CONTEXT_TAGS.includes(t) ? 'Behavioral tag — the agent acts on this' : 'Add tag'}>
                + {t}
              </button>
            ))}
          </div>
        )}
        <label className="ctx-pin" title="Pin: inject this into the agent's context at the start of every run">
          <input type="checkbox" checked={pinned} onChange={(e) => setPinned(e.target.checked)} disabled={busy} /> 📌 Pin for agents
        </label>

        <div className="ctx-compose-actions">
          <button className="ghost-btn" onClick={() => void addNote()} disabled={!online || busy || !noteBody.trim()}>{busy ? 'Saving…' : '＋ Add note'}</button>
          <span className="ctx-or">or attach a file</span>
          <label className={`filebtn ${busy ? 'busy' : ''}`}>
            {busy ? 'Uploading…' : '＋ Add file'}
            <input type="file" onChange={upload} disabled={!online || busy} hidden />
          </label>
        </div>
      </div>
    </section>
  )
}

// ContextView displays one context item in the center and lets you edit or delete it. Notes are text you
// rewrite in place; uploaded files show their content read-only (text inline, images inline, anything else a
// placeholder) while their name/tags/pin stay editable. Delete closes the document. The item is looked up
// live by id so edits/reloads stay in sync; a deleted item shows an unavailable notice.
function ContextView({
  item,
  online,
  reload,
  onError,
  onClose,
}: {
  item: ContextItem | null
  online: boolean
  reload: () => Promise<void>
  onError: (m: string) => void
  onClose: () => void
}) {
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState('')
  const [body, setBody] = useState('')
  const [tagsText, setTagsText] = useState('')
  const [pinned, setPinned] = useState(false)
  const [busy, setBusy] = useState(false)

  const isNote = item?.type === 'note'
  const artifactId = item?.artifact_id
  const isImage = !!item && /\.(png|jpe?g|gif|webp|svg|bmp)$/i.test(item.name)

  // Fetch text content for notes/files (images render via <img>, so skip the text fetch for them).
  useEffect(() => {
    if (!artifactId || !online || isImage) { setContent(null); return }
    let alive = true
    setLoading(true)
    api
      .artifactContent(artifactId)
      .then((t) => alive && setContent(t))
      .catch(() => alive && setContent(null))
      .finally(() => alive && setLoading(false))
    return () => { alive = false }
  }, [artifactId, online, isImage])

  function startEdit() {
    if (!item) return
    setName(item.name)
    setBody(content ?? '')
    setTagsText((item.tags ?? []).join(', '))
    setPinned(!!item.pinned)
    setEditing(true)
  }

  async function save() {
    if (!item) return
    const tags = tagsText.split(',').map((t) => t.trim().toLowerCase()).filter(Boolean)
    setBusy(true)
    try {
      await api.updateContext(item.id, { name: name.trim() || item.name, tags, pinned, ...(isNote ? { body } : {}) })
      if (isNote) setContent(body)
      await reload()
      setEditing(false)
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function del() {
    if (!item) return
    if (!window.confirm(`Delete "${item.name}"? This can't be undone.`)) return
    setBusy(true)
    try {
      await api.deleteContext(item.id)
      await reload()
      onClose()
    } catch (e) {
      onError((e as Error).message)
      setBusy(false)
    }
  }

  if (!item) return <div className="empty">This context item is no longer available.</div>

  const binary = content != null && content.includes('�')

  return (
    <section className="fd">
      <header className="fd-head">
        <div className="fd-titlerow">
          <span className="badge">{item.type}</span>
          {editing ? (
            <input className="ctx-note-title" value={name} onChange={(e) => setName(e.target.value)} disabled={busy} />
          ) : (
            <h2 className="fd-title">{item.pinned ? '📌 ' : ''}{item.name}</h2>
          )}
          <span className="grow" />
          {!editing ? (
            <>
              <button className="mini" disabled={!online || busy} onClick={startEdit}>✎ Edit</button>
              <button className="mini no" disabled={!online || busy} onClick={() => void del()}>🗑 Delete</button>
            </>
          ) : (
            <>
              <button className="mini ok" disabled={busy} onClick={() => void save()}>{busy ? 'Saving…' : 'Save'}</button>
              <button className="mini" disabled={busy} onClick={() => setEditing(false)}>Cancel</button>
            </>
          )}
        </div>
        {editing ? (
          <div className="ctx-edit-meta">
            <input className="ctx-tags" placeholder="tags, comma-separated" value={tagsText} onChange={(e) => setTagsText(e.target.value)} disabled={busy} />
            <label className="ctx-pin"><input type="checkbox" checked={pinned} onChange={(e) => setPinned(e.target.checked)} disabled={busy} /> 📌 Pin for agents</label>
          </div>
        ) : (
          (item.tags ?? []).length > 0 && (
            <div className="fd-meta">
              {(item.tags ?? []).map((t) => (
                <span key={t} className={`ctx-tag ${BEHAVIORAL_CONTEXT_TAGS.includes(t) ? 'behavioral' : ''}`}>{t}</span>
              ))}
            </div>
          )
        )}
      </header>

      {editing && isNote ? (
        <textarea className="ctx-view-editor" value={body} onChange={(e) => setBody(e.target.value)} disabled={busy} placeholder="Note text…" />
      ) : isImage ? (
        <img className="ctx-view-img" src={api.artifactContentURL(item.artifact_id)} alt={item.name} />
      ) : loading ? (
        <div className="empty">Loading…</div>
      ) : binary ? (
        <div className="empty">Binary file — no inline preview.</div>
      ) : (
        <pre className="ctx-view-body">{content}</pre>
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
  techniques,
  authWarn,
  online,
  afterFinding,
  onError,
}: {
  assets: { asset: Asset; appName: string }[]
  capabilities: CapabilityManifest[]
  techniques: Record<string, boolean> | null
  authWarn: string | null
  online: boolean
  afterFinding: () => Promise<void>
  onError: (m: string) => void
}) {
  const repoAssets = assets.filter((a) => a.asset.type === 'source_repo')
  // A capability is blocked when its technique isn't permitted by the engagement's rules (ADR-0051). When no
  // engagement/techniques are configured, nothing is gated (fail-open, matching the engine).
  const roeConfigured = !!techniques && Object.keys(techniques).length > 0
  const blockedCap = (c: CapabilityManifest) => !!c.technique && roeConfigured && !techniques![c.technique]
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
    const cap = capabilities.find((c) => c.id === capId)
    if (cap && blockedCap(cap)) {
      onError(`${cap.title} uses the ${cap.technique} technique, which this engagement does not permit.`)
      return
    }
    // Authorization soft-gate (ADR-0051): warn before running when authorization isn't on file / has expired.
    if (authWarn && !window.confirm(`${authWarn}\n\nRun anyway?`)) return
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
              <option key={c.id} value={c.id} disabled={blockedCap(c)}>
                {c.title}{blockedCap(c) ? ` — blocked (${c.technique} not permitted)` : ''}
              </option>
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

const FINDING_STATUSES = ['open', 'confirmed', 'remediated', 'accepted', 'false_positive']

const SEV_RANK: Record<string, number> = { critical: 4, high: 3, medium: 2, low: 1, info: 0 }
// Attribute keys worth surfacing as compact signal chips in the table, most-exploitable first.
const OBS_SIGNALS: { key: string; label: string }[] = [
  { key: 'reachable_confirmed', label: 'reachable✓' },
  { key: 'route_reachable', label: 'route→sink' },
  { key: 'reachable', label: 'reachable' },
  { key: 'exposed', label: 'exposed' },
  { key: 'verified', label: 'verified' },
  { key: 'outdated', label: 'outdated' },
]
const obsSignals = (o: Observation): string[] => {
  const a = o.attributes ?? {}
  const out = OBS_SIGNALS.filter((s) => a[s.key] === 'true').map((s) => s.label)
  if (a.triage_flag === 'true') out.unshift('🤖 flagged') // agent flagged for a human
  return out
}
const STATE_LABEL: Record<string, string> = { unreviewed: 'new', confirmed: 'promoted', rejected: 'dismissed' }

// ObservationsTab is the human triage queue: a compact, spreadsheet-style table of every raw signal the
// scanners/agents produced. Search/sort/filter to find what matters, select rows for bulk promote/investigate/
// dismiss, or click a row to inspect it in the side panel. Items already under investigation live on the
// Investigations tab, so they're excluded here.
function ObservationsTab({
  projectId,
  observations,
  online,
  onOpenCode,
  onJump,
  reload,
  onError,
}: {
  projectId: string
  observations: Observation[]
  online: boolean
  onOpenCode: OpenCode
  onJump: (t: Tab) => void
  reload: () => Promise<void>
  onError: (m: string) => void
}) {
  const [status, setStatus] = useState('triage')
  const [search, setSearch] = useState('')
  const [sevFilter, setSevFilter] = useState('all')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [detailId, setDetailId] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [investigating, setInvestigating] = useState<Set<string>>(new Set())
  const [triageNote, setTriageNote] = useState<string | null>(null)
  const pollRef = useRef<number | null>(null)

  async function loadInvestigating() {
    const inv = (await api.listInvestigations(projectId)) ?? []
    setInvestigating(new Set(inv.map((i: Investigation) => i.observation_id)))
  }
  useEffect(() => {
    if (online) void loadInvestigating().catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, projectId, observations])
  // Stop polling when the tab unmounts.
  useEffect(() => () => { if (pollRef.current) window.clearInterval(pollRef.current) }, [])

  // While a background triage runs, poll so its dismissals/flags/findings appear live. Auto-stops after a
  // few minutes (the agent's bounded run); the human can also just switch filters to refresh.
  function pollForTriage() {
    if (pollRef.current) window.clearInterval(pollRef.current)
    const stopAt = Date.now() + 4 * 60 * 1000
    pollRef.current = window.setInterval(() => {
      if (Date.now() > stopAt) {
        if (pollRef.current) window.clearInterval(pollRef.current)
        pollRef.current = null
        return
      }
      void reload().catch(() => {})
      void loadInvestigating().catch(() => {})
    }, 5000)
  }
  async function aiTriage(idsArg: string[] | null) {
    setBusy(true)
    try {
      const res = await api.startTriage(projectId, idsArg ?? undefined)
      setTriageNote(`🤖 AI triage running on ${res.queued} observation${res.queued === 1 ? '' : 's'} — dismissals, flags, and proposed findings will appear as it works.`)
      setSelected(new Set())
      setDetailId(null)
      pollForTriage()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // Under-investigation observations belong to the Investigations tab, not the triage queue.
  const visible = useMemo(() => observations.filter((o) => !investigating.has(o.id)), [observations, investigating])
  function triageAll() {
    const n = visible.filter((o) => o.review_state === 'unreviewed').length
    if (n > 0 && window.confirm(`Run AI triage on all ${n} untriaged observations? One agent works through them; you approve any findings it proposes.`)) void aiTriage(null)
  }
  const STATUS: { key: string; label: string; match: (o: Observation) => boolean }[] = [
    { key: 'triage', label: 'Needs triage', match: (o) => o.review_state === 'unreviewed' },
    { key: 'confirmed', label: 'Promoted', match: (o) => o.review_state === 'confirmed' },
    { key: 'dismissed', label: 'Dismissed', match: (o) => o.review_state === 'rejected' },
    { key: 'all', label: 'All', match: () => true },
  ]
  const activeStatus = STATUS.find((s) => s.key === status) ?? STATUS[0]
  const q = search.trim().toLowerCase()
  const rows = useMemo(
    () =>
      visible.filter((o) => {
        if (!activeStatus.match(o)) return false
        if (sevFilter !== 'all' && o.severity !== sevFilter) return false
        if (q && !`${o.title} ${o.rule_id ?? ''} ${o.location ?? ''} ${o.detail ?? ''}`.toLowerCase().includes(q)) return false
        return true
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [visible, status, sevFilter, q],
  )
  const detail = detailId ? observations.find((o) => o.id === detailId) ?? null : null

  async function runBulk(fn: (id: string) => Promise<unknown>, ids: string[]) {
    setBusy(true)
    try {
      for (const id of ids) await fn(id)
      await reload()
      await loadInvestigating()
      setSelected(new Set())
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  async function runOne(fn: (id: string) => Promise<unknown>, id: string) {
    setBusy(true)
    try {
      await fn(id)
      await reload()
      await loadInvestigating()
      setDetailId(null)
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const columns: Column<Observation>[] = [
    { key: 'severity', header: 'Sev', width: '72px', sortable: true, sortValue: (o) => SEV_RANK[o.severity] ?? -1, render: (o) => <span className={`sev sev-${o.severity}`}>{o.severity}</span> },
    { key: 'title', header: 'Title', sortable: true, sortValue: (o) => o.title.toLowerCase(), render: (o) => <span className="dt-title">{o.title}</span> },
    { key: 'rule', header: 'Rule', className: 'mono', width: '150px', sortable: true, sortValue: (o) => o.rule_id ?? '', render: (o) => <span className="muted dt-ellip">{o.rule_id}</span> },
    { key: 'location', header: 'Location', className: 'mono', width: '190px', sortable: true, sortValue: (o) => o.location ?? '', render: (o) => <span className="muted dt-ellip">{o.location}</span> },
    { key: 'signals', header: 'Signals', width: '150px', render: (o) => <span className="dt-signals">{obsSignals(o).map((s) => <span key={s} className="sig-chip">{s}</span>)}</span> },
    { key: 'state', header: 'State', width: '92px', sortable: true, sortValue: (o) => o.review_state, render: (o) => <span className={`badge ${o.review_state}`}>{STATE_LABEL[o.review_state] ?? o.review_state}</span> },
  ]

  const ids = [...selected]
  return (
    <div className="table-page">
      <div className="hero compact">
        <h1>Observations</h1>
        <p>
          The raw triage queue. Let{' '}
          <button className="link" disabled={!online || busy} onClick={triageAll}><b>🤖 AI triage</b></button>{' '}
          trawl it (dismisses noise by reachability, flags the real ones, proposes findings for your approval), or
          work rows yourself — promote to a{' '}
          <button className="link" onClick={() => onJump('findings')}>Finding</button> or dismiss. Click a row to inspect it.
        </p>
      </div>

      {triageNote && (
        <div className="banner">
          {triageNote}
          <button className="link" style={{ marginLeft: 10 }} onClick={() => void reload()}>Refresh</button>
          <button className="link" style={{ marginLeft: 10 }} onClick={() => setTriageNote(null)}>dismiss</button>
        </div>
      )}

      <div className="table-toolbar">
        <input className="tt-search" placeholder="Search title, rule, location…" value={search} onChange={(e) => setSearch(e.target.value)} />
        <select value={sevFilter} onChange={(e) => setSevFilter(e.target.value)}>
          <option value="all">All severities</option>
          {['critical', 'high', 'medium', 'low', 'info'].map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <div className="tt-chips">
          {STATUS.map((s) => (
            <button key={s.key} className={`chip ${status === s.key ? 'on' : ''}`} onClick={() => setStatus(s.key)}>
              {s.label} <span className="n">{visible.filter(s.match).length}</span>
            </button>
          ))}
        </div>
        <span className="grow" />
        <button
          className="ghost-btn"
          disabled={!online || busy}
          title="Run the AI triage agent over every untriaged observation"
          onClick={triageAll}
        >
          🤖 Triage all
        </button>
        <span className="muted tt-count">{rows.length} shown</span>
      </div>

      {selected.size > 0 && (
        <div className="bulk-bar">
          <span><b>{selected.size}</b> selected</span>
          <button className="mini ok" disabled={!online || busy} onClick={() => void runBulk((id) => api.promoteObservation(id), ids)}>⚑ Promote</button>
          <button className="mini" disabled={!online || busy} title="Hand these to the AI triage agent" onClick={() => void aiTriage(ids)}>🤖 AI triage</button>
          <button className="mini no" disabled={!online || busy} onClick={() => void runBulk((id) => api.reviewObservation(id, 'rejected'), ids)}>Dismiss</button>
          <button className="mini" disabled={busy} onClick={() => setSelected(new Set())}>clear</button>
          {busy && <span className="muted">working…</span>}
        </div>
      )}

      <div className="table-split">
        <DataTable
          rows={rows}
          columns={columns}
          selectable
          selected={selected}
          onSelectChange={setSelected}
          onRowClick={(o) => setDetailId(o.id)}
          activeId={detail?.id}
          defaultSort={{ key: 'severity', dir: 'desc' }}
          empty={status === 'triage' ? 'Nothing left to triage. 🎉' : 'No observations match.'}
        />
        {detail && (
          <ObservationDetailPanel
            obs={detail}
            online={online}
            busy={busy}
            onClose={() => setDetailId(null)}
            onOpenCode={onOpenCode}
            onAction={(fn) => void runOne(fn, detail.id)}
            onTriage={() => void aiTriage([detail.id])}
          />
        )}
      </div>
    </div>
  )
}

// ObservationDetailPanel is the side panel shown when a table row is clicked: the full observation plus the
// same triage actions, so you can inspect before acting without leaving the table.
function ObservationDetailPanel({
  obs,
  online,
  busy,
  onClose,
  onOpenCode,
  onAction,
  onTriage,
}: {
  obs: Observation
  online: boolean
  busy: boolean
  onClose: () => void
  onOpenCode: OpenCode
  onAction: (fn: (id: string) => Promise<unknown>) => void
  onTriage: () => void
}) {
  // Show the AI's triage rationale prominently; keep it out of the raw attribute dump below.
  const rationale = obs.attributes?.triage_rationale
  const attrs = Object.entries(obs.attributes ?? {}).filter(([k]) => k !== 'triage_rationale' && k !== 'triaged_by' && k !== 'triage_flag')
  return (
    <aside className="detail-panel">
      <div className="dp-head">
        <span className={`sev sev-${obs.severity}`}>{obs.severity}</span>
        <span className="dp-title">{obs.title}</span>
        <button className="dp-close" onClick={onClose} aria-label="Close">✕</button>
      </div>
      <div className="dp-body">
        <div className="dp-meta">
          <span className={`badge ${obs.review_state}`}>{STATE_LABEL[obs.review_state] ?? obs.review_state}</span>
          <span className="muted">origin: {obs.origin}</span>
        </div>
        {rationale && (
          <div className="dp-triage">
            <span className="dp-k">{obs.attributes?.triaged_by === 'agent' ? '🤖 AI triage' : 'Triage'}</span>
            <span>{rationale}</span>
          </div>
        )}
        {obs.rule_id && <div className="dp-row"><span className="dp-k">Rule</span><span className="mono">{obs.rule_id}</span></div>}
        {obs.location && <div className="dp-row"><span className="dp-k">Location</span><LocationChip obs={obs} onOpenCode={onOpenCode} /></div>}
        {obs.detail && <p className="dp-detail">{obs.detail}</p>}
        {attrs.length > 0 && (
          <div className="dp-attrs">
            {attrs.map(([k, v]) => <span key={k} className="sig-chip mono">{k}={v}</span>)}
          </div>
        )}
      </div>
      <div className="dp-actions">
        {obs.review_state === 'unreviewed' ? (
          <>
            <button className="mini ok" disabled={!online || busy} onClick={() => onAction((id) => api.promoteObservation(id))}>⚑ Promote</button>
            <button className="mini" disabled={!online || busy} title="Hand this observation to the AI triage agent" onClick={onTriage}>🤖 AI triage</button>
            <button className="mini no" disabled={!online || busy} onClick={() => onAction((id) => api.reviewObservation(id, 'rejected'))}>Dismiss</button>
          </>
        ) : (
          <button className="mini" disabled={!online || busy} onClick={() => onAction((id) => api.reviewObservation(id, 'unreviewed'))}>↺ Restore to triage</button>
        )}
      </div>
    </aside>
  )
}

function FindingsTab({
  findings,
  observations,
  onOpenFinding,
  onJump,
  reload,
  onError,
  focusId,
  focusNonce,
}: {
  findings: Finding[]
  observations: Observation[]
  onOpenFinding: (f: Finding) => void
  onJump: (t: Tab) => void
  reload: () => Promise<void>
  onError: (m: string) => void
  focusId?: string
  focusNonce?: number
}) {
  // Index observations by id so each finding can show its supporting location (findings carry no location of
  // their own — it lives on the observations they were promoted from, ADR-0050).
  const byId = useMemo(() => new Map(observations.map((o) => [o.id, o])), [observations])
  const firstLoc = (f: Finding): string => {
    for (const id of f.observation_ids) {
      const o = byId.get(id)
      if (o?.location) return o.location
    }
    return ''
  }
  // Deep-link from omni-search: briefly flash the focused finding row.
  const [flash, setFlash] = useState<string | null>(null)
  useEffect(() => {
    if (!focusId) return
    setFlash(focusId)
    const t = setTimeout(() => setFlash(null), 1600)
    return () => clearTimeout(t)
  }, [focusId, focusNonce])

  const [search, setSearch] = useState('')
  const [sevFilter, setSevFilter] = useState('all')
  const q = search.trim().toLowerCase()
  const rows = useMemo(
    () =>
      findings.filter((f) => {
        if (sevFilter !== 'all' && f.severity !== sevFilter) return false
        if (q && !`${f.title} ${f.cwe ?? ''} ${firstLoc(f)}`.toLowerCase().includes(q)) return false
        return true
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [findings, sevFilter, q, byId],
  )

  const columns: Column<Finding>[] = [
    { key: 'severity', header: 'Sev', width: '72px', sortable: true, sortValue: (f) => SEV_RANK[f.severity] ?? -1, render: (f) => <span className={`sev sev-${f.severity}`}>{f.severity}</span> },
    { key: 'title', header: 'Title', sortable: true, sortValue: (f) => f.title.toLowerCase(), render: (f) => <span className="dt-title">{f.title}</span> },
    { key: 'cwe', header: 'CWE', width: '104px', sortable: true, sortValue: (f) => f.cwe ?? '', render: (f) => <span className="muted mono">{f.cwe}</span> },
    { key: 'location', header: 'Location', className: 'mono', width: '190px', render: (f) => <span className="muted dt-ellip">{firstLoc(f)}</span> },
    {
      key: 'status', header: 'Status', width: '148px', sortable: true, sortValue: (f) => f.status,
      render: (f) => (
        <select
          className={`finding-status badge ${f.status}`}
          value={f.status}
          title="Finding status"
          onClick={(e) => e.stopPropagation()}
          onChange={async (e) => {
            try {
              await api.setFindingStatus(f.id, e.target.value)
              await reload()
            } catch (err) {
              onError((err as Error).message)
            }
          }}
        >
          {FINDING_STATUSES.map((s) => <option key={s} value={s}>{s.replace('_', ' ')}</option>)}
        </select>
      ),
    },
  ]

  return (
    <div className="table-page">
      <div className="hero compact">
        <h1>Findings</h1>
        <p>
          Confirmed, report-worthy vulnerabilities. Nothing lands here on its own — you promote it from{' '}
          <button className="link" onClick={() => onJump('observations')}>Observations</button> or confirm it in{' '}
          <button className="link" onClick={() => onJump('investigations')}>Investigations</button>. Click a finding to open its detail.
        </p>
      </div>
      <div className="table-toolbar">
        <input className="tt-search" placeholder="Search title, CWE, location…" value={search} onChange={(e) => setSearch(e.target.value)} />
        <select value={sevFilter} onChange={(e) => setSevFilter(e.target.value)}>
          <option value="all">All severities</option>
          {['critical', 'high', 'medium', 'low', 'info'].map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <span className="grow" />
        <span className="muted tt-count">{rows.length} of {findings.length}</span>
      </div>
      <DataTable
        rows={rows}
        columns={columns}
        onRowClick={(f) => onOpenFinding(f)}
        getRowClass={(f) => (flash === f.id ? 'flash' : '')}
        defaultSort={{ key: 'severity', dir: 'desc' }}
        empty="No findings yet. Triage scanner results in the Observations tab and promote the real ones here."
      />
    </div>
  )
}

// FindingDetail is a finding's info page, opened as a document when you click a finding row. It shows the
// finding's description, status (editable), CWE, and every supporting observation the finding was promoted
// from — each with its scanner rule, detail, routing signals, source locations (jump-to-file ↦ chips) and
// reachability. The finding is looked up live from the loaded set so status edits and reloads stay in sync;
// if it disappears (deleted elsewhere) we say so rather than render a blank.
function FindingDetail({
  projectId,
  finding,
  observations,
  online,
  onOpenCode,
  reload,
  onError,
}: {
  projectId: string
  finding: Finding | null
  observations: Observation[]
  online: boolean
  onOpenCode: OpenCode
  reload: () => Promise<void>
  onError: (m: string) => void
}) {
  const byId = useMemo(() => new Map(observations.map((o) => [o.id, o])), [observations])
  if (!finding) return <div className="empty">This finding is no longer available.</div>
  const obs = finding.observation_ids.map((id) => byId.get(id)).filter((o): o is Observation => !!o)
  // Facts worth surfacing on an observation, minus the ones already shown structurally (locations/flow).
  const signalKeys = (o: Observation) =>
    Object.keys(o.attributes ?? {}).filter((k) => k !== 'dataflow_source' && k !== 'dataflow_path')
  return (
    <section className="fd">
      <header className="fd-head">
        <div className="fd-titlerow">
          <span className={`sev sev-${finding.severity}`}>{finding.severity}</span>
          <h2 className="fd-title">{finding.title}</h2>
          <span className="grow" />
          <select
            className={`finding-status badge ${finding.status}`}
            value={finding.status}
            title="Finding status"
            onChange={async (e) => {
              try {
                await api.setFindingStatus(finding.id, e.target.value)
                await reload()
              } catch (err) {
                onError((err as Error).message)
              }
            }}
          >
            {FINDING_STATUSES.map((s) => (
              <option key={s} value={s}>{s.replace('_', ' ')}</option>
            ))}
          </select>
        </div>
        <div className="fd-meta muted">
          {finding.cwe && <span>{finding.cwe}</span>}
          <span>{obs.length} supporting observation{obs.length === 1 ? '' : 's'}</span>
          <span>opened {new Date(finding.created_at).toLocaleString()}</span>
        </div>
      </header>

      {finding.description && <p className="fd-desc">{finding.description}</p>}

      <h3 className="fd-section">Evidence</h3>
      {obs.length === 0 ? (
        <div className="empty">No supporting observations are attached to this finding.</div>
      ) : (
        <ul className="fd-obs">
          {obs.map((o) => (
            <li key={o.id} className="fd-ob">
              <div className="fd-ob-head">
                <span className={`sev sev-${o.severity}`}>{o.severity}</span>
                <span className="row-title">{o.title}</span>
                {o.rule_id && <span className="muted mono">{o.rule_id}</span>}
              </div>
              {o.detail && <div className="fd-ob-detail">{o.detail}</div>}
              {o.location && (
                <div className="loc-row">
                  <LocationChip obs={o} onOpenCode={onOpenCode} />
                </div>
              )}
              {signalKeys(o).length > 0 && (
                <div className="fd-signals">
                  {signalKeys(o).map((k) => (
                    <span key={k} className="fd-signal mono">{k}={o.attributes![k]}</span>
                  ))}
                </div>
              )}
              <FindingReachability projectId={projectId} subject={o.id} online={online} onError={onError} />
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
