import { useCallback, useEffect, useState } from 'react'
import { api, ConnectionModel, ModelRef, ModelRouting as Routing, ProviderView } from './api'

// ModelRouting maps a routing tag to a (connection, model) — cross-provider, so `cheap` can run on a local
// model and `reasoning` on a strong one (ADR-0021). The model picker draws from each connection's live,
// discovered model set (ADR-0052), not a static catalog. A profile's tag selects its row; scope + DLP apply.
export function ModelRouting({ online }: { online: boolean }) {
  const [providers, setProviders] = useState<ProviderView[]>([])
  const [tags, setTags] = useState<string[]>([])
  const [routing, setRouting] = useState<Routing>({ tags: {} })
  // Discovered models per connection id, loaded lazily as connections are referenced.
  const [modelsByConn, setModelsByConn] = useState<Record<string, ConnectionModel[]>>({})

  const loadModels = useCallback(
    (connId: string) => {
      if (!connId || !online) return
      setModelsByConn((prev) => (connId in prev ? prev : { ...prev, [connId]: [] })) // mark in-flight
      void api
        .getConnectionModels(connId)
        .then((r) => setModelsByConn((prev) => ({ ...prev, [connId]: r.models ?? [] })))
        .catch(() => {})
    },
    [online],
  )

  useEffect(() => {
    if (!online) return
    void Promise.all([api.listProviders(), api.getModelRouting()])
      .then(([ps, r]) => {
        setProviders(ps ?? [])
        setTags(r.tags ?? [])
        setRouting(r.routing ?? { tags: {} })
        // Prefetch models for every connection already referenced by the routing map.
        const refs = [r.routing?.default, ...Object.values(r.routing?.tags ?? {})]
        new Set(refs.map((x) => x?.provider_id).filter(Boolean) as string[]).forEach(loadModels)
      })
      .catch(() => {})
  }, [online, loadModels])

  const refFor = (tag: string): ModelRef =>
    (tag === 'default' ? routing.default : routing.tags?.[tag]) ?? { provider_id: '', model: '' }

  async function setRef(tag: string, ref: ModelRef) {
    const next: Routing = { default: routing.default, tags: { ...(routing.tags ?? {}) } }
    if (tag === 'default') next.default = ref
    else next.tags![tag] = ref
    setRouting(next)
    try {
      await api.setModelRouting(next)
    } catch {
      /* keep optimistic */
    }
  }

  function priceLabel(m: ConnectionModel): string {
    if (!m.input_per_mtok && !m.output_per_mtok) return m.family || 'local'
    return `$${m.input_per_mtok}/$${m.output_per_mtok}`
  }

  const rows = ['default', ...tags]

  return (
    <div className="routing">
      <div className="prov-add-title">Model routing</div>
      <div className="routing-note">
        Right-size the model per task — a profile's tag picks its row here. Cross-provider: use a local model
        for <code>cheap</code>, a strong one for <code>reasoning</code>. Models come from each connection's
        live list (↻ Refresh under Models &amp; Providers). Scope &amp; data-egress limits still apply.
      </div>
      {providers.length === 0 && <div className="agents-empty">Add a connection first (Models &amp; Providers).</div>}
      {rows.map((tag) => {
        const ref = refFor(tag)
        const models = modelsByConn[ref.provider_id]
        return (
          <div key={tag} className="routing-row">
            <span className="routing-tag">{tag}</span>
            <select
              value={ref.provider_id}
              onChange={(e) => {
                loadModels(e.target.value)
                setRef(tag, { provider_id: e.target.value, model: '' })
              }}
              disabled={!online}
            >
              <option value="">— unset —</option>
              {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            {models && models.length > 0 ? (
              <select value={ref.model} onChange={(e) => setRef(tag, { ...ref, model: e.target.value })} disabled={!online || !ref.provider_id}>
                <option value="">connection default</option>
                {models.map((m) => (
                  <option key={m.model_id} value={m.model_id}>{m.display_name} · {priceLabel(m)}</option>
                ))}
              </select>
            ) : (
              <input placeholder="model id" value={ref.model} onChange={(e) => setRef(tag, { ...ref, model: e.target.value })} disabled={!online || !ref.provider_id} />
            )}
          </div>
        )
      })}
    </div>
  )
}
