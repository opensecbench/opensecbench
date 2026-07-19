import { useEffect, useState } from 'react'
import { api, AgentProfile } from './api'

type Step = { key: string; profile: string; instruction: string; depends_on: string[] }

// PlaybookBuilder authors an agent playbook from scratch — name, goal, and a list of steps, each a
// sub-task for a specialist profile with dependencies on earlier steps (ADR-0019 step 4).
export function PlaybookBuilder({ online, onSaved, onCancel }: { online: boolean; onSaved: () => void; onCancel: () => void }) {
  const [profiles, setProfiles] = useState<AgentProfile[]>([])
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [goal, setGoal] = useState('')
  const [steps, setSteps] = useState<Step[]>([{ key: 'step1', profile: '', instruction: '', depends_on: [] }])
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    void api
      .listAgentProfiles()
      .then((ps) => {
        const specialists = ps.filter((p) => p.id !== 'lead' && p.id !== 'generalist')
        setProfiles(specialists)
        setSteps((s) => s.map((st) => ({ ...st, profile: st.profile || specialists[0]?.id || '' })))
      })
      .catch((e) => setError((e as Error).message))
  }, [])

  function update(i: number, patch: Partial<Step>) {
    setSteps((s) => s.map((st, j) => (j === i ? { ...st, ...patch } : st)))
  }
  function addStep() {
    setSteps((s) => [...s, { key: `step${s.length + 1}`, profile: profiles[0]?.id ?? '', instruction: '', depends_on: [] }])
  }
  function removeStep(i: number) {
    setSteps((s) => s.filter((_, j) => j !== i))
  }
  function toggleDep(i: number, key: string) {
    setSteps((s) =>
      s.map((st, j) =>
        j === i ? { ...st, depends_on: st.depends_on.includes(key) ? st.depends_on.filter((k) => k !== key) : [...st.depends_on, key] } : st,
      ),
    )
  }

  async function save() {
    setError(null)
    setSaving(true)
    try {
      await api.createAgentPlaybook({ name: name.trim(), description: description.trim(), goal: goal.trim(), steps })
      onSaved()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="pbuild">
      <div className="pbuild-h">
        <b>New playbook</b>
        <span className="grow" />
        <button className="ghost-btn" onClick={onCancel}>Cancel</button>
      </div>
      {error && <div className="banner error">⚠ {error}</div>}
      <input className="pbuild-in" placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} />
      <input className="pbuild-in" placeholder="Goal (what this playbook achieves)" value={goal} onChange={(e) => setGoal(e.target.value)} />
      <input className="pbuild-in" placeholder="Description (optional)" value={description} onChange={(e) => setDescription(e.target.value)} />

      {steps.map((st, i) => (
        <div key={i} className="pbuild-step">
          <div className="pbuild-step-h">
            <input className="pbuild-key" placeholder="key" value={st.key} onChange={(e) => update(i, { key: e.target.value })} />
            <select value={st.profile} onChange={(e) => update(i, { profile: e.target.value })}>
              {profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            <span className="grow" />
            {steps.length > 1 && <button className="orch-del" title="Remove step" onClick={() => removeStep(i)}>×</button>}
          </div>
          <textarea
            className="pbuild-instr"
            placeholder="Instruction for the specialist — tell it to read existing state first, then do only what's needed."
            value={st.instruction}
            onChange={(e) => update(i, { instruction: e.target.value })}
          />
          {i > 0 && (
            <div className="pbuild-deps">
              <span>after</span>
              {steps.slice(0, i).map((prev) => (
                <button
                  key={prev.key}
                  className={st.depends_on.includes(prev.key) ? 'on' : ''}
                  onClick={() => toggleDep(i, prev.key)}
                >
                  {prev.key || '(unnamed)'}
                </button>
              ))}
            </div>
          )}
        </div>
      ))}

      <div className="pbuild-actions">
        <button className="ghost-btn" onClick={addStep}>＋ Add step</button>
        <span className="grow" />
        <button className="pbuild-save" disabled={!online || saving || !name.trim()} onClick={save}>
          {saving ? 'Saving…' : 'Save playbook'}
        </button>
      </div>
    </div>
  )
}
