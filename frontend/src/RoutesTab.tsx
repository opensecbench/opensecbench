import { useMemo } from 'react'
import { Observation, RouteView } from './api'
import { OpenCode, parseLoc } from './CodeLink'

function sevColor(s?: string): string {
  return { critical: '#7c1d1d', high: '#dc2626', medium: '#f59e0b', low: '#3b82f6', info: '#6b7280' }[s ?? ''] ?? '#3a4560'
}

// RoutesTab is the attack-surface detail pane (ADR-0033/0034, workbench split). The route inventory —
// search, filter, selection — lives in the left Explorer (RoutesExplorer); this pane inspects the one route
// picked there. A route reads as an attack path: where it's handled, whether traffic has confirmed it, and
// which findings are provably reachable from it (route→sink). Reachable-finding sinks open in the code viewer.
export function RoutesTab({
  routes,
  selectedRouteId,
  observations,
  online,
  onReload,
  onOpenCode,
  onJump,
}: {
  routes: RouteView[]
  selectedRouteId: string | null
  observations: Observation[]
  online: boolean
  onReload: () => void
  onOpenCode: OpenCode
  onJump: (t: string) => void
}) {
  const obsById = useMemo(() => new Map(observations.map((o) => [o.id, o])), [observations])
  const route = routes.find((r) => r.id === selectedRouteId) ?? null

  if (!route) {
    return (
      <div className="content routes-detail">
        <div className="empty">
          {routes.length === 0 ? (
            <>No routes yet — run the <b>route-map</b> capability (part of “Scan everything”) to map the entry points.</>
          ) : (
            'Pick a route from the sidebar to inspect it.'
          )}
        </div>
      </div>
    )
  }

  const findings = route.findings ?? []
  return (
    <div className="content routes-detail">
      <div className="route-detail-head">
        <span className={`route-method m-${(route.method || 'any').toLowerCase()}`}>{route.method || 'ANY'}</span>
        <span className="route-detail-path mono">{route.path}</span>
        {route.observed && <span className="route-obs" title="Confirmed against captured traffic">✔ live</span>}
        <span className="grow" />
        <button className="ghost-btn" disabled={!online} onClick={onReload}>Refresh</button>
      </div>

      <div className="route-detail-meta">
        {route.handler_file && (
          <div className="rdm-row">
            <span className="rdm-k">handler</span>
            <span className="rdm-v mono">{route.handler_file}{route.handler_line ? `:${route.handler_line}` : ''}</span>
          </div>
        )}
        {route.framework && (
          <div className="rdm-row">
            <span className="rdm-k">framework</span>
            <span className="rdm-v">{route.framework}</span>
          </div>
        )}
        <div className="rdm-row">
          <span className="rdm-k">status</span>
          <span className="rdm-v">{route.observed ? 'traffic-confirmed' : 'not yet observed in traffic'}</span>
        </div>
      </div>

      <h2 className="route-detail-h">Findings {findings.length > 0 && <span className="muted">({findings.length})</span>}</h2>
      {findings.length === 0 ? (
        <div className="empty small">No findings are attached to this route.</div>
      ) : (
        <ul className="route-findings detail">
          {findings.map((f) => {
            const obs = obsById.get(f.observation_id)
            const loc = obs?.location ? parseLoc(obs.location) : null
            return (
              <li key={f.observation_id} className="route-finding">
                <span className="rf-dot" style={{ background: sevColor(f.severity) }} />
                <span className="rf-title">{f.title}</span>
                <span className="rf-sev">{f.severity}</span>
                {f.reachable === 'reachable' ? (
                  <span className="rf-reach" title={`Reachability: ${f.reach_confidence} confidence · ${(f.reach_sources ?? []).join(', ')}`}>
                    ↯ reachable{f.reach_confidence ? ` (${f.reach_confidence})` : ''}
                  </span>
                ) : f.reachable === 'unreachable' ? (
                  <span className="rf-unreach" title={`Determined unreachable · ${(f.reach_sources ?? []).join(', ')}`}>unreachable</span>
                ) : f.route_reachable ? (
                  <span className="rf-reach" title="A dataflow path runs from this route to the sink">↯ reachable</span>
                ) : null}
                {(f.reach_sources?.length ?? 0) > 0 && <span className="rf-src">{f.reach_sources!.join(' · ')}</span>}
                <span className="grow" />
                {loc && obs?.asset_id && (
                  <button className="link" onClick={() => onOpenCode(obs.asset_id!, loc.path, loc.line)}>view sink →</button>
                )}
                <button className="link" onClick={() => onJump('findings')}>view finding →</button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
