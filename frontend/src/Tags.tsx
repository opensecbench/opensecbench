import { useCallback, useEffect, useState } from 'react'
import { api, ConnectionModel, ModelRef, ModelRouting, ProviderView } from './api'

// Built-in task types that run on each tag (mirrors pkg/analyst/profiles.go) — shown so the operator sees
// what a tag drives. Display-only; the runtime resolution lives in the backend.
const TAG_USED_BY: Record<string, string[]> = {
  default: ['chat', 'lead', 'code-analysis', 'tech-scout', 'scribe'],
  cheap: ['triage', 'report-writer', 'narrator'],
  reasoning: ['pentester', 'vuln-validator', 'assessor'],
  fast: [],
  'long-context': [],
}

// Tags is the routing surface (ADR-0052): each tag owns an ordered priority list of (connection, model).
// The top entry is used first; the rest are fall-through candidates. Tagging a model = adding it to a
// tag's list; reordering = priority. Built-in tags drive the task types shown; custom tags are freeform.
export function Tags({ online }: { online: boolean }) {
  const [providers, setProviders] = useState<ProviderView[]>([])
  const [builtins, setBuiltins] = useState<string[]>([])
  const [routing, setRouting] = useState<ModelRouting>({})
  const [extra, setExtra] = useState<string[]>([]) // custom tags added this session (may be empty)
  const [models, setModels] = useState<Record<string, ConnectionModel[]>>({})
  const [newTag, setNewTag] = useState('')

  const loadModels = useCallback(
    (connId: string) => {
      if (!connId || !online) return
      setModels((prev) => (connId in prev ? prev : { ...prev, [connId]: [] }))
      void api
        .getConnectionModels(connId)
        .then((r) => setModels((p) => ({ ...p, [connId]: r.models ?? [] })))
        .catch(() => {})
    },
    [online],
  )

  useEffect(() => {
    if (!online) return
    void Promise.all([api.listProviders(), api.getModelRouting()])
      .then(([ps, r]) => {
        setProviders(ps ?? [])
        setBuiltins(r.tags ?? [])
        setRouting(r.routing ?? {})
        const ids = new Set<string>()
        Object.values(r.routing ?? {}).forEach((list) => list.forEach((e) => e.provider_id && ids.add(e.provider_id)))
        ids.forEach(loadModels)
      })
      .catch(() => {})
  }, [online, loadModels])

  const connName = (id: string) => providers.find((p) => p.id === id)?.name ?? id
  const connType = (id: string) => providers.find((p) => p.id === id)?.type ?? ''

  async function save(next: ModelRouting) {
    setRouting(next)
    try {
      await api.setModelRouting(next)
    } catch {
      /* keep optimistic */
    }
  }
  function setList(tag: string, list: ModelRef[]) {
    save({ ...routing, [tag]: list })
  }
  function move(tag: string, i: number, dir: -1 | 1) {
    const list = [...(routing[tag] ?? [])]
    const j = i + dir
    if (j < 0 || j >= list.length) return
    ;[list[i], list[j]] = [list[j], list[i]]
    setList(tag, list)
  }
  function remove(tag: string, i: number) {
    const list = (routing[tag] ?? []).filter((_, k) => k !== i)
    setList(tag, list)
  }
  function addCustomTag() {
    const t = newTag.trim().toLowerCase().replace(/\s+/g, '-')
    if (t && !allTags.includes(t)) setExtra((e) => [...e, t])
    setNewTag('')
  }

  const allTags = Array.from(new Set([...builtins, ...Object.keys(routing), ...extra]))

  return (
    <div className="tags-zone">
      <div className="prov-add-title">Tags · ordered — top model is used, falls through if unavailable</div>
      <div className="routing-note">
        A tag is a priority list of models across connections. The first one is used; drag order (▲▼) sets
        which wins when several can do the job. Built-in tags drive the task types shown; add your own for
        anything else. Scope &amp; data-egress limits still apply.
      </div>
      {providers.length === 0 && <div className="agents-empty">Add a connection first (above).</div>}

      {allTags.map((tag) => {
        const list = routing[tag] ?? []
        const builtin = builtins.includes(tag)
        const used = TAG_USED_BY[tag]
        return (
          <div key={tag} className="tagcard">
            <div className="tag-head">
              <span className="tagname">{tag}</span>
              <span className={`tagkind ${builtin ? 'builtin' : 'custom'}`}>{builtin ? 'built-in' : 'custom'}</span>
              <span className="tag-usedby">
                {used === undefined ? '' : used.length ? <>runs: <b>{used.join(', ')}</b></> : 'no task type — inherits default'}
              </span>
            </div>
            <div className="plist">
              {list.map((e, i) => (
                <div key={`${e.provider_id}:${e.model}:${i}`} className="pitem">
                  <span className="rank">{i + 1}</span>
                  <span className="pmodel">
                    <span className="mid">{e.model || '(connection default)'}</span>
                    {i === 0 && <span className="primary-badge">used first</span>}
                    <span className="via">via {connName(e.provider_id)}{connType(e.provider_id) ? ` · ${connType(e.provider_id)}` : ''}</span>
                  </span>
                  <span className="pmove">
                    <button className="ghost-btn" disabled={i === 0} onClick={() => move(tag, i, -1)}>▲</button>
                    <button className="ghost-btn" disabled={i === list.length - 1} onClick={() => move(tag, i, 1)}>▼</button>
                    <button className="del" title="Remove" onClick={() => remove(tag, i)}>✕</button>
                  </span>
                </div>
              ))}
              <AddModelRow providers={providers} models={models} loadModels={loadModels} online={online} onAdd={(ref) => setList(tag, [...list, ref])} />
            </div>
          </div>
        )
      })}

      <div className="new-tag-row">
        <input
          placeholder="new custom tag (e.g. vision, local-only)"
          value={newTag}
          onChange={(e) => setNewTag(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && addCustomTag()}
          disabled={!online}
        />
        <button className="ghost-btn" onClick={addCustomTag} disabled={!online || !newTag.trim()}>+ add tag</button>
      </div>
    </div>
  )
}

// AddModelRow picks a connection then a model from its discovered set and appends it to a tag's list.
function AddModelRow({
  providers,
  models,
  loadModels,
  online,
  onAdd,
}: {
  providers: ProviderView[]
  models: Record<string, ConnectionModel[]>
  loadModels: (id: string) => void
  online: boolean
  onAdd: (ref: ModelRef) => void
}) {
  const [conn, setConn] = useState('')
  const [model, setModel] = useState('')
  const list = models[conn]
  return (
    <div className="add-model-row">
      <select
        value={conn}
        onChange={(e) => {
          setConn(e.target.value)
          setModel('')
          loadModels(e.target.value)
        }}
        disabled={!online}
      >
        <option value="">+ add a model…</option>
        {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
      </select>
      {conn &&
        (list && list.length > 0 ? (
          <select value={model} onChange={(e) => setModel(e.target.value)}>
            <option value="">connection default</option>
            {list.map((m) => <option key={m.model_id} value={m.model_id}>{m.display_name || m.model_id}</option>)}
          </select>
        ) : (
          <input placeholder="model id" value={model} onChange={(e) => setModel(e.target.value)} />
        ))}
      {conn && (
        <button
          className="ghost-btn"
          onClick={() => {
            onAdd({ provider_id: conn, model })
            setConn('')
            setModel('')
          }}
        >
          add
        </button>
      )}
    </div>
  )
}
