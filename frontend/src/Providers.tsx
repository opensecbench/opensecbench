import { useEffect, useState } from 'react'
import { api, ModelCatalogEntry, ProviderView, UsageByModel } from './api'

// Map a provider add-form type to its catalog provider key (Azure shares OpenAI's models; claude-cli
// picks its own model, so it has no picker).
export const CATALOG_KEY: Record<string, string> = { anthropic: 'anthropic', openai: 'openai', azure: 'openai', deepseek: 'deepseek', grok: 'grok', ollama: 'ollama' }

function priceLabel(m: ModelCatalogEntry): string {
  if (m.input_per_mtok === 0 && m.output_per_mtok === 0) return 'local'
  return `$${m.input_per_mtok}/$${m.output_per_mtok} per Mtok`
}

// Provider types offered in the add form. needsBase/needsKey drive which fields show; the hint steers
// the operator (API keys recommended, claude-cli uses the local subscription, ollama is local).
export const PROVIDER_TYPES = [
  { value: 'anthropic', label: 'Anthropic API', needsKey: true, needsBase: false, modelHint: 'claude-sonnet-5' },
  { value: 'openai', label: 'OpenAI-compatible', needsKey: true, needsBase: true, modelHint: 'gpt-5' },
  { value: 'ollama', label: 'Ollama (local)', needsKey: false, needsBase: true, modelHint: 'llama3' },
  { value: 'claude-cli', label: 'Claude CLI (subscription)', needsKey: false, needsBase: false, modelHint: '' },
  { value: 'deepseek', label: 'DeepSeek', needsKey: true, needsBase: false, modelHint: 'deepseek-v4-flash' },
  { value: 'grok', label: 'xAI Grok', needsKey: true, needsBase: false, modelHint: 'grok-4-fast' },
]

type TestState = { ok?: boolean; latency_ms?: number; sample?: string; error?: string; testing?: boolean }

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

  useEffect(() => {
    if (online) void api.getModelCatalog().then(setModelCatalog).catch(() => {})
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
      await api.addProvider({ name: name || cfg?.label || type, type, model, base_url: baseUrl, api_key: apiKey })
      setName('')
      setModel('')
      setBaseUrl('')
      setApiKey('')
      setError(null)
      await load()
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
                {t && !t.testing && (
                  t.ok ? (
                    <div className="prov-test ok">✓ {t.latency_ms}ms · {t.sample}</div>
                  ) : (
                    <div className="prov-test err">✕ {t.error}</div>
                  )
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
        {cfg?.needsBase && <input placeholder="base URL" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />}
        {cfg?.needsKey && <input type="password" placeholder="API key (sealed in the vault)" value={apiKey} onChange={(e) => setApiKey(e.target.value)} />}
        <button className="prov-add-btn" onClick={add} disabled={!online}>＋ Add provider</button>
        <div className="prov-hint">
          {type === 'claude-cli'
            ? 'Uses your local `claude` login (a Claude subscription), driven headless in JSON mode.'
            : type === 'ollama'
              ? 'Local models — no key, nothing leaves your machine.'
              : 'An API key is the most reliable path. Keys are sealed in the vault, never stored in the clear.'}
        </div>
      </div>
    </div>
  )
}
