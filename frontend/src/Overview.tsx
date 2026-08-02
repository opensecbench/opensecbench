import { useEffect, useState } from 'react'
import { api, CoverageView, Engagement, Finding, Project, ProjectSummary } from './api'

const SEV_COLOR: Record<string, string> = { critical: '#7c1d1d', high: '#dc2626', medium: '#f59e0b', low: '#3b82f6', info: '#6b7280' }

// OverviewTab is the per-project start page — status at a glance plus a "Start here" checklist that
// orients someone who opens a project and doesn't know where to begin (ADR IA declutter). Each step
// reflects real state and jumps to the surface that advances it.
export function OverviewTab({
  project,
  assets,
  findings,
  coverage,
  engagement,
  online,
  onJump,
  onScan,
}: {
  project: Project
  assets: number
  findings: Finding[]
  coverage: CoverageView | null
  engagement: Engagement | null
  online: boolean
  onJump: (t: string) => void
  onScan: () => Promise<{ enqueued: unknown[]; skipped: unknown[] }>
}) {
  const [scanBusy, setScanBusy] = useState(false)
  const [scanMsg, setScanMsg] = useState<string | null>(null)
  const [summary, setSummary] = useState<ProjectSummary | null>(null)
  useEffect(() => {
    if (!online) return
    api.projectSummary(project.id).then(setSummary).catch(() => {})
  }, [online, project.id, findings.length])
  async function runScan() {
    setScanBusy(true)
    setScanMsg(null)
    try {
      const res = await onScan()
      const n = res.enqueued.length
      setScanMsg(n === 0 ? 'No applicable scanners for the current assets.' : `Started ${n} scan${n === 1 ? '' : 's'}. Watch them in Tasks; findings will appear as they finish.`)
    } catch (e) {
      setScanMsg(`Scan failed: ${(e as Error).message}`)
    } finally {
      setScanBusy(false)
    }
  }
  const covered = coverage?.summary.covered_pct ?? 0
  const clDone = coverage?.summary.covered ?? 0
  const clTotal = coverage?.summary.total ?? 0
  const adopted = (coverage?.packs ?? []).length > 0
  const hasEngagement = !!engagement
  const openFindings = findings.filter((f) => f.status !== 'resolved' && f.status !== 'closed').length

  const steps = [
    {
      done: hasEngagement,
      to: 'engagement',
      t: 'Define scope & engagement',
      d: hasEngagement ? 'Targets and authorization on file.' : 'Set targets, rules of engagement, and authorization first.',
      go: 'Settings',
    },
    {
      done: assets > 0,
      to: 'assets',
      t: 'Bring in assets',
      d: assets > 0 ? `${assets} asset${assets === 1 ? '' : 's'} imported.` : 'Add the apps, hosts, repos, and endpoints in scope.',
      go: 'Assets',
    },
    {
      done: findings.length > 0,
      to: 'scan',
      scan: true,
      t: 'Scan everything',
      d: assets > 0 ? 'Run every applicable scanner across your assets — SBOM, CVEs, SAST, routes, secrets — auto-triaged into findings. No agent needed.' : 'Add assets first, then scan them all in one click.',
      go: 'Scan',
    },
    {
      done: adopted,
      to: 'methodology',
      t: 'Add a checklist',
      d: adopted ? `${clDone} of ${clTotal} worked through.` : 'Add a checklist to work through — WSTG, ASVS, or your own.',
      go: 'Checklist',
    },
    {
      done: false,
      to: 'orchestrate',
      t: 'Run the Analyst or a playbook',
      d: 'Let an agent investigate, or run a saved playbook on this project.',
      go: 'Agents',
    },
    {
      done: false,
      to: 'findings',
      t: 'Triage findings → report',
      d: openFindings > 0 ? `${openFindings} open finding${openFindings === 1 ? '' : 's'} to review.` : 'Confirm findings, then generate the report.',
      go: 'Findings',
    },
  ]
  // The first not-yet-done step is the current focus.
  const nowIdx = steps.findIndex((s) => !s.done)

  return (
    <div className="wb-ov">
      <div className="wb-ov-crumbs">{project.name} · assessment</div>
      <h2 className="wb-ov-h">Overview</h2>
      <p className="wb-ov-lede">Where the assessment stands and what to do next. Every tool is on the left, grouped by what it's for — but you don't have to hunt. Start here.</p>

      <div className="wb-ov-stats">
        <button className="wb-ov-stat" onClick={() => onJump('assets')} title="View assets"><div className="v">{assets}</div><div className="k">assets</div></button>
        <button className="wb-ov-stat" onClick={() => onJump('findings')} title="View findings"><div className={`v ${findings.length ? 'crit' : ''}`}>{findings.length}</div><div className="k">findings</div></button>
        <button className="wb-ov-stat" onClick={() => onJump('methodology')} title="View checklist"><div className={`v ${adopted ? (covered >= 80 ? 'good' : 'warn') : ''}`}>{adopted ? `${clDone}/${clTotal}` : '—'}</div><div className="k">checklist</div></button>
        <button className="wb-ov-stat" onClick={() => onJump('engagement')} title="Scope & engagement"><div className={`v ${hasEngagement ? 'good' : 'warn'}`}>{hasEngagement ? '✓' : '—'}</div><div className="k">engagement</div></button>
      </div>

      {summary && (summary.findings.total > 0 || summary.dependencies.Total > 0) && (
        <div className="ov-rollup">
          <div className="ov-rollup-h">What we've found</div>
          <div className="ov-rollup-grid">
            <button className="ovr-card" onClick={() => onJump('findings')} title="View findings">
              <div className="ovr-k">Findings</div>
              <div className="ovr-sevs">
                {['critical', 'high', 'medium', 'low'].map((sev) =>
                  summary.findings[sev] ? (
                    <span key={sev} className="ovr-sev" style={{ background: SEV_COLOR[sev] }}>{summary.findings[sev]} {sev}</span>
                  ) : null,
                )}
                {!summary.findings.total && <span className="ovr-none">none yet</span>}
              </div>
              <div className="ovr-sub">{summary.reachable} reachable · {summary.open_investigations} open investigation{summary.open_investigations === 1 ? '' : 's'}</div>
            </button>
            <button className="ovr-card" onClick={() => onJump('routes')} title="View the attack surface">
              <div className="ovr-k">Attack surface</div>
              <div className="ovr-big">{summary.routes.Total} <span className="ovr-unit">routes</span></div>
              <div className="ovr-sub">{summary.routes.Exposed} traffic-confirmed · <b className={summary.routes.WithFindings ? 'crit' : ''}>{summary.routes.WithFindings} with findings</b></div>
            </button>
            <button className="ovr-card" onClick={() => onJump('graph')} title="View the dependency graph">
              <div className="ovr-k">Dependencies</div>
              <div className="ovr-big">{summary.dependencies.Total}</div>
              <div className="ovr-sub"><b className={summary.dependencies.Vulnerabilities ? 'crit' : ''}>{summary.dependencies.Vulnerabilities} vulnerable</b> · {summary.dependencies.Outdated} outdated</div>
            </button>
          </div>
        </div>
      )}

      <div className="wb-ov-starth">Start here</div>
      <div className="wb-ov-steps">
        {steps.map((s, i) => (
          <div key={s.t} className={`wb-ov-step ${s.done ? 'done' : i === nowIdx ? 'now' : ''}`}>
            <div className="mark">{s.done ? '✓' : i === nowIdx ? '▸' : i + 1}</div>
            <div className="body">
              <div className="t">{s.t}</div>
              <div className="d">{'scan' in s && s.scan && scanMsg ? scanMsg : s.d}</div>
            </div>
            {'scan' in s && s.scan ? (
              <button className="go" disabled={!online || scanBusy || assets === 0} onClick={runScan}>{scanBusy ? 'Scanning…' : s.go}</button>
            ) : (
              <button className="go" onClick={() => onJump(s.to)}>{s.go}</button>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
