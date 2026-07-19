import { useEffect, useState } from 'react'
import { api, ModelCatalogEntry, ModelRef, ModelRouting as Routing, ProviderView } from './api'
import { CATALOG_KEY } from './Providers'

// ModelRouting maps a routing tag to a (provider, model) — cross-provider, so `cheap` can run on a local
// model and `reasoning` on a strong one (ADR-0021). A profile's tag selects its row; scope + DLP still apply.
export function ModelRouting({ online }: { online: boolean }) {
  const [providers, setProviders] = useState<ProviderView[]>([])
  const [catalog, setCatalog] = useState<ModelCatalogEntry[]>([])
  const [tags, setTags] = useState<string[]>([])
  const [routing, setRouting] = useState<Routing>({ tags: {} })

  useEffect(() => {
    if (!online) return
    void Promise.all([api.listProviders(), api.getModelCatalog(), api.getModelRouting()])
      .then(([ps, cat, r]) => {
        setProviders(ps ?? [])
        setCatalog(cat)
        setTags(r.tags ?? [])
        setRouting(r.routing ?? { tags: {} })
      })
      .catch(() => {})
  }, [online])

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

  const modelsFor = (providerId: string) => {
    const type = providers.find((p) => p.id === providerId)?.type ?? ''
    return catalog.filter((m) => m.provider === CATALOG_KEY[type])
  }

  const rows = ['default', ...tags]

  return (
    <div className="routing">
      <div className="prov-add-title">Model routing</div>
      <div className="routing-note">
        Right-size the model per task — a profile's tag picks its row here. Cross-provider: use a local model
        for <code>cheap</code>, a strong one for <code>reasoning</code>. Scope &amp; data-egress limits still apply.
      </div>
      {providers.length === 0 && <div className="agents-empty">Add a provider first (Models &amp; Providers).</div>}
      {rows.map((tag) => {
        const ref = refFor(tag)
        const models = modelsFor(ref.provider_id)
        return (
          <div key={tag} className="routing-row">
            <span className="routing-tag">{tag}</span>
            <select value={ref.provider_id} onChange={(e) => setRef(tag, { provider_id: e.target.value, model: '' })} disabled={!online}>
              <option value="">— unset —</option>
              {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            {models.length > 0 ? (
              <select value={ref.model} onChange={(e) => setRef(tag, { ...ref, model: e.target.value })} disabled={!online || !ref.provider_id}>
                <option value="">provider default</option>
                {models.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}
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
