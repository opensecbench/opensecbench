import { useEffect, useState } from 'react'
import { api, ClassificationLevel, ConnectionModel, DataClearance, ModelCatalogEntry, ProviderView, UsageByModel } from './api'

// Map a provider add-form type to its catalog provider key. The Claude CLI subscription runs the same
// Anthropic models a direct API connection does (passed as --model, ADR-0052), so it shares Anthropic's
// list. Azure shares OpenAI's. The Bedrock/Foundry gateways serve many families, so they have no single
// overlay list — their models come from discovery.
export const CATALOG_KEY: Record<string, string> = { anthropic: 'anthropic', 'claude-cli': 'anthropic', openai: 'openai', azure: 'openai', deepseek: 'deepseek', grok: 'grok', ollama: 'ollama', bedrock: '', 'azure-foundry': '' }

function priceLabel(m: ModelCatalogEntry): string {
  if (m.input_per_mtok === 0 && m.output_per_mtok === 0) return 'local'
  return `$${m.input_per_mtok}/$${m.output_per_mtok} per Mtok`
}

// Provider types offered in the add form. needsBase/needsKey drive which fields show; the hints steer the
// operator. baseHint/keyHint override the generic field placeholders — Bedrock reuses the base field for
// its AWS region and the key field for AWS credentials (ADR-0052).
export const PROVIDER_TYPES = [
  { value: 'anthropic', label: 'Anthropic API', needsKey: true, needsBase: false, modelHint: 'claude-sonnet-5', baseHint: '', keyHint: '' },
  { value: 'openai', label: 'OpenAI-compatible', needsKey: true, needsBase: true, modelHint: 'gpt-5', baseHint: '', keyHint: '' },
  { value: 'ollama', label: 'Ollama (local)', needsKey: false, needsBase: true, modelHint: 'llama3', baseHint: '', keyHint: '' },
  { value: 'claude-cli', label: 'Claude CLI (subscription)', needsKey: false, needsBase: false, modelHint: 'claude-sonnet-5', baseHint: '', keyHint: '' },
  { value: 'deepseek', label: 'DeepSeek', needsKey: true, needsBase: false, modelHint: 'deepseek-v4-flash', baseHint: '', keyHint: '' },
  { value: 'grok', label: 'xAI Grok', needsKey: true, needsBase: false, modelHint: 'grok-4-fast', baseHint: '', keyHint: '' },
  { value: 'bedrock', label: 'AWS Bedrock (gateway)', needsKey: true, needsBase: true, modelHint: 'default model (optional)', baseHint: 'AWS region · e.g. us-east-1', keyHint: 'blank = use ~/.aws / env / SSO · profile:NAME · or ACCESS_KEY_ID:SECRET[:TOKEN]' },
  { value: 'azure-foundry', label: 'Azure AI Foundry (gateway)', needsKey: true, needsBase: true, modelHint: 'default model (optional)', baseHint: 'endpoint · e.g. https://<res>.services.ai.azure.com/models', keyHint: '' },
]

type TestState = { ok?: boolean; latency_ms?: number; sample?: string; error?: string; testing?: boolean; requested_model?: string; served_model?: string }

// Providers lists/configures inference providers. projectId is optional — the per-project token-usage
// table shows only when it's given (e.g. from a project's Analyst dock, not global settings).
export function Providers({ online, projectId, onChanged }: { online: boolean; projectId?: string; onChanged?: () => void }) {
  const [providers, setProviders] = useState<ProviderView[]>([])
  const [usage, setUsage] = useState<UsageByModel[]>([])
  const [type, setType] = useState('anthropic')
  const [name, setName] = useState('')
  const [model, setModel] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [tests, setTests] = useState<Record<string, TestState>>({})
  const [error, setError] = useState<string | null>(null)
  const [modelCatalog, setModelCatalog] = useState<ModelCatalogEntry[]>([])
  const [customModel, setCustomModel] = useState(false)
  const [clearance, setClearance] = useState<DataClearance>('') // add-form clearance; defaults to the least tier once loaded
  // In-progress clearance-note edits, keyed by connection id or `${connId}:${modelId}` for model overrides.
  const [clearNotes, setClearNotes] = useState<Record<string, string>>({})
  // The data-classification scale (governance) drives the clearance options; ordered least → most sensitive.
  const [levels, setLevels] = useState<ClassificationLevel[]>([])
  const labelOf = (id: string) => levels.find((l) => l.id === id)?.label ?? id
  // Discovered models per connection (ADR-0052): lazily fetched, expandable, refreshable.
  const [expanded, setExpanded] = useState<string | null>(null)
  const [connModels, setConnModels] = useState<Record<string, ConnectionModel[]>>({})
  const [refreshedAt, setRefreshedAt] = useState<Record<string, string>>({})
  const [modelsBusy, setModelsBusy] = useState<Record<string, boolean>>({})

  async function loadConnModels(id: string, force: boolean) {
    setModelsBusy((b) => ({ ...b, [id]: true }))
    try {
      const r = force ? await api.refreshConnectionModels(id) : await api.getConnectionModels(id)
      setConnModels((m) => ({ ...m, [id]: r.models ?? [] }))
      setRefreshedAt((m) => ({ ...m, [id]: r.refreshed_at }))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setModelsBusy((b) => ({ ...b, [id]: false }))
    }
  }
  function toggleModels(id: string) {
    if (expanded === id) {
      setExpanded(null)
      return
    }
    setExpanded(id)
    if (!(id in connModels)) void loadConnModels(id, false)
  }

  useEffect(() => {
    if (online) void api.getModelCatalog().then(setModelCatalog).catch(() => {})
  }, [online])

  useEffect(() => {
    if (!online) return
    void api.listClassificationLevels().then((ls) => {
      setLevels(ls ?? [])
      if (ls?.length) setClearance((c) => c || ls[0].id) // default the add form to the least-sensitive tier
    }).catch(() => {})
  }, [online])

  async function load() {
    try {
      setProviders((await api.listProviders()) ?? [])
      if (projectId) setUsage((await api.getProjectUsage(projectId)) ?? [])
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    if (online) void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, projectId])

  const cfg = PROVIDER_TYPES.find((t) => t.value === type)

  async function add() {
    try {
      await api.addProvider({ name: name || cfg?.label || type, type, model, base_url: baseUrl, api_key: apiKey, data_clearance: clearance })
      setName('')
      setModel('')
      setBaseUrl('')
      setApiKey('')
      setClearance(levels[0]?.id ?? '')
      setError(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }
  async function saveClearance(id: string, next: DataClearance, note: string) {
    try {
      await api.setProviderClearance(id, next, note)
      await load()
      onChanged?.()
    } catch (e) {
      setError((e as Error).message)
    }
  }
  async function saveModelClearance(connId: string, modelId: string, next: DataClearance | '', note: string) {
    try {
      await api.setModelClearance(connId, modelId, next, note)
      await loadConnModels(connId, false)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  async function activate(id: string) {
    try {
      await api.activateProvider(id)
      await load()
      onChanged?.()
    } catch (e) {
      setError((e as Error).message)
    }
  }
  async function test(id: string) {
    setTests((t) => ({ ...t, [id]: { ...t[id], testing: true } }))
    try {
      const r = await api.testProvider(id)
      setTests((t) => ({ ...t, [id]: { ...r, testing: false } }))
    } catch (e) {
      setTests((t) => ({ ...t, [id]: { ok: false, error: (e as Error).message, testing: false } }))
    }
  }
  async function remove(id: string) {
    try {
      await api.deleteProvider(id)
      await load()
      onChanged?.()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="prov-body">
      {error && <div className="banner error">⚠ {error}</div>}

      <div className="prov-list">
        {providers.length === 0 && <div className="empty">No providers configured. Add one below.</div>}
        {providers.map((p) => {
          const t = tests[p.id]
          return (
            <div key={p.id} className={`prov-item ${p.active ? 'active' : ''}`}>
              <div className="prov-main">
                <div className="prov-name">{p.name} {p.active && <span className="badge active">active</span>}</div>
                <div className="prov-meta">{p.type}{p.model ? ` · ${p.model}` : ''}{p.has_key ? ' · 🔑' : ''}</div>
                {p.type === 'ollama' ? (
                  <div className="prov-clearance local">🔒 Local · content stays on your machine</div>
                ) : (
                  <div className="prov-clearance">
                    <span className="prov-clearance-label" title="Highest asset-sensitivity tier this destination may receive over external egress.">Data clearance</span>
                    <select value={p.data_clearance} onChange={(e) => saveClearance(p.id, e.target.value as DataClearance, clearNotes[p.id] ?? p.clearance_note)} disabled={!online}>
                      {levels.map((l) => <option key={l.id} value={l.id}>{l.label}</option>)}
                    </select>
                    <input
                      className="prov-clearance-note"
                      placeholder="why (e.g. covered by DPA)"
                      value={clearNotes[p.id] ?? p.clearance_note}
                      onChange={(e) => setClearNotes((n) => ({ ...n, [p.id]: e.target.value }))}
                      onBlur={(e) => { if (e.target.value !== p.clearance_note) void saveClearance(p.id, p.data_clearance, e.target.value) }}
                      disabled={!online}
                    />
                  </div>
                )}
                {t && !t.testing && (
                  t.ok ? (
                    <div className="prov-test ok">
                      ✓ {t.latency_ms}ms · {t.sample}
                      {t.served_model && (
                        <div className={`prov-served ${t.requested_model && t.requested_model !== t.served_model ? 'mismatch' : ''}`}>
                          served: {t.served_model}
                          {t.requested_model && t.requested_model !== t.served_model && <> · requested: {t.requested_model || '(none)'} ⚠</>}
                        </div>
                      )}
                    </div>
                  ) : (
                    <div className="prov-test err">✕ {t.error}</div>
                  )
                )}
                <button className="ghost-btn prov-models-toggle" onClick={() => toggleModels(p.id)} disabled={!online}>
                  {expanded === p.id ? '▾' : '▸'} models{connModels[p.id] ? ` (${connModels[p.id].length})` : ''}
                </button>
                {expanded === p.id && (
                  <div className="prov-models">
                    <div className="prov-models-head">
                      <span>
                        {(connModels[p.id]?.length ?? 0)} models
                        {refreshedAt[p.id] ? ` · ${new Date(refreshedAt[p.id]).toLocaleString()}` : ''}
                      </span>
                      <button className="ghost-btn" onClick={() => loadConnModels(p.id, true)} disabled={modelsBusy[p.id]}>
                        {modelsBusy[p.id] ? '…' : '↻ refresh'}
                      </button>
                    </div>
                    {(connModels[p.id] ?? []).map((m) => (
                      <div key={m.model_id} className="prov-model-row">
                        <span className="mono">{m.model_id}</span>
                        <span className="prov-model-meta">
                          {m.source}
                          {m.context_window ? ` · ${Math.round(m.context_window / 1000)}k` : ''}
                          {m.input_per_mtok || m.output_per_mtok ? ` · $${m.input_per_mtok}/$${m.output_per_mtok}` : ''}
                        </span>
                        {p.type !== 'ollama' && (
                          <span className="prov-model-clearance">
                            <select
                              value={m.data_clearance}
                              onChange={(e) => saveModelClearance(p.id, m.model_id, e.target.value as DataClearance | '', clearNotes[`${p.id}:${m.model_id}`] ?? m.clearance_note)}
                              disabled={!online}
                              title="Clear this model for a lower tier than its connection (e.g. a model with retention not covered by the DPA)."
                            >
                              <option value="">inherit · {labelOf(p.data_clearance)}</option>
                              {levels.map((l) => <option key={l.id} value={l.id}>{l.label}</option>)}
                            </select>
                            {m.data_clearance && (
                              <input
                                className="prov-clearance-note"
                                placeholder="why (e.g. 30-day retention)"
                                value={clearNotes[`${p.id}:${m.model_id}`] ?? m.clearance_note}
                                onChange={(e) => setClearNotes((n) => ({ ...n, [`${p.id}:${m.model_id}`]: e.target.value }))}
                                onBlur={(e) => { if (e.target.value !== m.clearance_note) void saveModelClearance(p.id, m.model_id, m.data_clearance, e.target.value) }}
                                disabled={!online}
                              />
                            )}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
              <div className="prov-actions">
                {!p.active && <button className="ghost-btn" onClick={() => activate(p.id)}>activate</button>}
                <button className="ghost-btn" onClick={() => test(p.id)} disabled={t?.testing}>{t?.testing ? '…' : 'test'}</button>
                <button className="del" title="Delete" onClick={() => remove(p.id)}>✕</button>
              </div>
            </div>
          )
        })}
      </div>

      {usage.length > 0 && (
        <div className="prov-usage">
          <div className="prov-add-title">Token usage · this project</div>
          <table className="prov-usage-table">
            <thead>
              <tr><th>vendor · model</th><th>runs</th><th>in</th><th>out</th></tr>
            </thead>
            <tbody>
              {usage.map((u) => (
                <tr key={u.provider + u.model}>
                  <td className="mono">{u.provider}{u.model ? ` · ${u.model}` : ''}</td>
                  <td>{u.runs}</td>
                  <td>{u.input_tokens.toLocaleString()}</td>
                  <td>{u.output_tokens.toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="prov-add">
        <div className="prov-add-title">Add provider</div>
        <select value={type} onChange={(e) => { setType(e.target.value); setModel(''); setCustomModel(false) }}>
          {PROVIDER_TYPES.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
        </select>
        <input placeholder="name (optional)" value={name} onChange={(e) => setName(e.target.value)} />
        {(() => {
          const catModels = modelCatalog.filter((m) => m.provider === CATALOG_KEY[type])
          if (catModels.length > 0 && !customModel) {
            return (
              <select value={model} onChange={(e) => {
                if (e.target.value === '__custom__') { setCustomModel(true); setModel('') }
                else setModel(e.target.value)
              }}>
                <option value="">default model</option>
                {catModels.map((m) => <option key={m.id} value={m.id}>{m.name} · {priceLabel(m)}</option>)}
                <option value="__custom__">Custom…</option>
              </select>
            )
          }
          return <input placeholder={cfg?.modelHint ? `model · e.g. ${cfg.modelHint}` : 'model (optional)'} value={model} onChange={(e) => setModel(e.target.value)} />
        })()}
        {cfg?.needsBase && <input placeholder={cfg?.baseHint || 'base URL'} value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />}
        {cfg?.needsKey && <input type="password" placeholder={cfg?.keyHint || 'API key (sealed in the vault)'} value={apiKey} onChange={(e) => setApiKey(e.target.value)} />}
        {type !== 'ollama' && (
          <select value={clearance} onChange={(e) => setClearance(e.target.value as DataClearance)} title="Highest data tier this destination may receive over external egress. Default is least-privilege; raise it per your agreement with the vendor.">
            {levels.map((l) => <option key={l.id} value={l.id}>Cleared for: {l.label}</option>)}
          </select>
        )}
        <button className="prov-add-btn" onClick={add} disabled={!online}>＋ Add provider</button>
        <div className="prov-hint">
          {type === 'claude-cli'
            ? 'Uses your local `claude` login (a Claude subscription) as a native Anthropic API backend via its OAuth token — full tool-use, like an API key. Run `claude` once to log in.'
            : type === 'ollama'
              ? 'Local models — no key, nothing leaves your machine.'
              : type === 'bedrock'
                ? 'Leave the key blank to use your ambient AWS credentials (the standard chain: ~/.aws/credentials & AWS_PROFILE, env vars, SSO, IAM roles) — refreshed automatically. Use profile:NAME for a specific profile, or paste a static access key. Region is required.'
                : 'An API key is the most reliable path. Keys are sealed in the vault, never stored in the clear.'}
        </div>
      </div>
    </div>
  )
}
