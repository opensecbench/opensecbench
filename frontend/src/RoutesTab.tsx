import { useCallback, useEffect, useState } from 'react'
import { api, Project, RouteView } from './api'

function sevColor(s?: string): string {
  return { critical: '#7c1d1d', high: '#dc2626', medium: '#f59e0b', low: '#3b82f6', info: '#6b7280' }[s ?? ''] ?? '#3a4560'
}

// RoutesTab is the attack-surface view (ADR-0033/0034): the HTTP entry-point inventory, ranked by the
// risk reachable from each route. A route reads as an attack path — which endpoints exist, which are
// traffic-confirmed, and which findings are provably reachable from them (route→sink).
export function RoutesTab({ project, online, onError, onJump }: { project: Project; online: boolean; onError: (m: string) => void; onJump: (t: string) => void }) {
  const [routes, setRoutes] = useState<RouteView[]>([])
  const [loaded, setLoaded] = useState(false)
  const [observedOnly, setObservedOnly] = useState(false)
  const [withFindings, setWithFindings] = useState(false)
  const [open, setOpen] = useState<Record<string, boolean>>({})

  const load = useCallback(async () => {
    try {
      setRoutes((await api.projectRoutes(project.id)) ?? [])
      setLoaded(true)
    } catch (e) {
      onError((e as Error).message)
    }
  }, [project.id, onError])

  useEffect(() => {
    if (online) void load()
  }, [online, load])

  const shown = routes.filter((r) => (!observedOnly || r.observed) && (!withFindings || (r.findings?.length ?? 0) > 0))
  const confirmed = routes.filter((r) => r.observed).length
  const exploitable = routes.filter((r) => r.reachable_count > 0).length

  return (
    <div className="content routes">
      <div className="hero">
        <h1>Attack surface</h1>
        <p>
          {routes.length} route{routes.length === 1 ? '' : 's'} · {confirmed} traffic-confirmed · <b style={{ color: exploitable ? '#dc2626' : 'inherit' }}>{exploitable} with a reachable finding</b>. Entry points ranked by the risk behind them.
        </p>
      </div>

      <div className="routes-filters">
        <button className={`chip ${observedOnly ? 'on' : ''}`} onClick={() => setObservedOnly((v) => !v)}>✔ observed only</button>
        <button className={`chip ${withFindings ? 'on' : ''}`} onClick={() => setWithFindings((v) => !v)}>⚑ with findings</button>
        <span className="grow" />
        <button className="ghost-btn" disabled={!online} onClick={() => void load()}>Refresh</button>
      </div>

      {loaded && routes.length === 0 && (
        <div className="empty">No routes yet — run the <b>route-map</b> capability (part of “Scan everything”) to map the entry points.</div>
      )}
      {loaded && routes.length > 0 && shown.length === 0 && <div className="empty">No routes match the filters.</div>}

      <div className="routes-list">
        {shown.map((r) => {
          const findings = r.findings ?? []
          const expandable = findings.length > 0
          const isOpen = !!open[r.id]
          return (
            <div key={r.id} className={`route-row ${r.reachable_count > 0 ? 'risk' : ''}`}>
              <div className="route-head" onClick={() => expandable && setOpen((o) => ({ ...o, [r.id]: !o[r.id] }))} style={{ cursor: expandable ? 'pointer' : 'default' }}>
                <span className={`route-method m-${r.method.toLowerCase() || 'any'}`}>{r.method || 'ANY'}</span>
                <span className="route-path mono">{r.path}</span>
                {r.observed && <span className="route-obs" title="Confirmed against captured traffic">✔ live</span>}
                <span className="grow" />
                {r.handler_file && <span className="route-handler mono muted">{r.handler_file}{r.handler_line ? `:${r.handler_line}` : ''}</span>}
                {r.framework && <span className="route-fw">{r.framework}</span>}
                {findings.length > 0 ? (
                  <span className="route-risk" style={{ background: sevColor(r.worst_severity) }}>
                    {r.reachable_count > 0 ? `${r.reachable_count} reachable` : `${findings.length} finding${findings.length === 1 ? '' : 's'}`}
                  </span>
                ) : (
                  <span className="route-clean">clean</span>
                )}
                {expandable && <span className="route-caret">{isOpen ? '▾' : '▸'}</span>}
              </div>
              {isOpen && (
                <ul className="route-findings">
                  {findings.map((f) => (
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
                      <button className="link" onClick={() => onJump('findings')}>view →</button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
