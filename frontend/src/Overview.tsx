import { CoverageView, Engagement, Finding, Project } from './api'

// OverviewTab is the per-project start page — status at a glance plus a "Start here" checklist that
// orients someone who opens a project and doesn't know where to begin (ADR IA declutter). Each step
// reflects real state and jumps to the surface that advances it.
export function OverviewTab({
  project,
  assets,
  findings,
  coverage,
  engagement,
  onJump,
}: {
  project: Project
  assets: number
  findings: Finding[]
  coverage: CoverageView | null
  engagement: Engagement | null
  onJump: (t: string) => void
}) {
  const covered = coverage?.summary.covered_pct ?? 0
  const adopted = (coverage?.packs ?? []).length > 0
  const hasEngagement = !!engagement
  const openFindings = findings.filter((f) => f.status !== 'resolved' && f.status !== 'closed').length

  const steps = [
    {
      done: hasEngagement,
      to: 'scope',
      t: 'Define scope & engagement',
      d: hasEngagement ? 'Targets and authorization on file.' : 'Set targets, rules of engagement, and authorization first.',
      go: 'Scope',
    },
    {
      done: assets > 0,
      to: 'assets',
      t: 'Bring in assets',
      d: assets > 0 ? `${assets} asset${assets === 1 ? '' : 's'} imported.` : 'Add the apps, hosts, repos, and endpoints in scope.',
      go: 'Assets',
    },
    {
      done: adopted,
      to: 'methodology',
      t: 'Adopt a methodology',
      d: adopted ? `Tracking coverage — ${covered}% so far.` : 'Pick a testing standard to track coverage against.',
      go: 'Methodology',
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
        <div className="wb-ov-stat"><div className="v">{assets}</div><div className="k">assets</div></div>
        <div className="wb-ov-stat"><div className={`v ${findings.length ? 'crit' : ''}`}>{findings.length}</div><div className="k">findings</div></div>
        <div className="wb-ov-stat"><div className={`v ${adopted ? (covered >= 80 ? 'good' : 'warn') : ''}`}>{adopted ? `${covered}%` : '—'}</div><div className="k">coverage</div></div>
        <div className="wb-ov-stat"><div className={`v ${hasEngagement ? 'good' : 'warn'}`}>{hasEngagement ? '✓' : '—'}</div><div className="k">engagement</div></div>
      </div>

      <div className="wb-ov-starth">Start here</div>
      <div className="wb-ov-steps">
        {steps.map((s, i) => (
          <div key={s.t} className={`wb-ov-step ${s.done ? 'done' : i === nowIdx ? 'now' : ''}`}>
            <div className="mark">{s.done ? '✓' : i === nowIdx ? '▸' : i + 1}</div>
            <div className="body">
              <div className="t">{s.t}</div>
              <div className="d">{s.d}</div>
            </div>
            <button className="go" onClick={() => onJump(s.to)}>{s.go}</button>
          </div>
        ))}
      </div>
    </div>
  )
}
