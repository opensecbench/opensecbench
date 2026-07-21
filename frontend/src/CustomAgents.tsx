import { useEffect, useState } from 'react'
import { api, AgentProfile, AgentTool } from './api'

// CustomAgents lists user-defined agent profiles and builds new ones — a persona plus a least-privilege
// tool allow-list (ADR-0019 step 4). The safety invariants are appended server-side and can't be dropped.
export function CustomAgents({ online }: { online: boolean }) {
  const [profiles, setProfiles] = useState<AgentProfile[]>([])
  const [tools, setTools] = useState<AgentTool[]>([])
  const [building, setBuilding] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [persona, setPersona] = useState('')
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [modelTag, setModelTag] = useState('') // routing tag; '' inherits the default list (ADR-0052)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  async function load() {
    try {
      const [ps, ts] = await Promise.all([api.listAgentProfiles(), api.listAgentTools()])
      setProfiles(ps)
      setTools(ts)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    if (online) void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  const saved = profiles.filter((p) => !p.builtin)

  function toggle(tool: string) {
    setPicked((s) => {
      const n = new Set(s)
      if (n.has(tool)) n.delete(tool)
      else n.add(tool)
      return n
    })
  }

  async function save() {
    setError(null)
    setSaving(true)
    try {
      await api.createAgentProfile({ name: name.trim(), description: description.trim(), persona: persona.trim(), tools: [...picked], model_tag: modelTag })
      setName('')
      setDescription('')
      setPersona('')
      setPicked(new Set())
      setModelTag('')
      setBuilding(false)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  async function remove(id: string) {
    try {
      await api.deleteAgentProfile(id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="agents">
      <div className="prov-add-title agents-h">
        <span>Custom agents</span>
        <span className="grow" />
        <button className="orch-new" disabled={!online} onClick={() => setBuilding((v) => !v)}>{building ? 'Close' : '＋ New'}</button>
      </div>
      {error && <div className="banner error">⚠ {error}</div>}

      {saved.length === 0 && !building && <div className="agents-empty">No custom agents yet. The built-in ones cover most work.</div>}
      {saved.map((p) => (
        <div key={p.id} className="agents-row">
          <div>
            <div className="agents-name">{p.name}</div>
            <div className="agents-tools">{(p.tools ?? []).length} tools</div>
          </div>
          <span className="grow" />
          <button className="orch-del" title="Delete" onClick={() => remove(p.id)}>×</button>
        </div>
      ))}

      {building && (
        <div className="pbuild">
          <input className="pbuild-in" placeholder="Name (e.g. Cloud Reviewer)" value={name} onChange={(e) => setName(e.target.value)} />
          <input className="pbuild-in" placeholder="Description (optional)" value={description} onChange={(e) => setDescription(e.target.value)} />
          <textarea className="pbuild-instr" placeholder="Persona — how this agent should behave and what its job is." value={persona} onChange={(e) => setPersona(e.target.value)} />
          <div className="agents-tools-label">Tools (least privilege — pick only what it needs)</div>
          <div className="agents-tool-grid">
            {tools.map((t) => (
              <label key={t.name} className={picked.has(t.name) ? 'on' : ''} title={t.description}>
                <input type="checkbox" checked={picked.has(t.name)} onChange={() => toggle(t.name)} />
                {t.name}
              </label>
            ))}
          </div>
          <div className="agents-tools-label">Model — which tag's models this agent runs on</div>
          <select className="pbuild-in" value={modelTag} onChange={(e) => setModelTag(e.target.value)}>
            <option value="">default routing (inherit)</option>
            <option value="cheap">cheap</option>
            <option value="fast">fast</option>
            <option value="reasoning">reasoning</option>
            <option value="long-context">long-context</option>
          </select>
          <div className="pbuild-actions">
            <span className="grow" />
            <button className="pbuild-save" disabled={!online || saving || !name.trim() || !persona.trim() || picked.size === 0} onClick={save}>
              {saving ? 'Saving…' : 'Save agent'}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
