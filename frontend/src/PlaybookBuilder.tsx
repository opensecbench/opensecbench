import { useEffect, useState } from 'react'
import { api, AgentPlaybook, AgentProfile } from './api'

type Step = { key: string; profile: string; instruction: string; depends_on: string[]; gate: boolean }

// PlaybookBuilder authors OR edits an agent playbook (ADR-0019 §4): name, goal, and a list of steps. Each
// step is either a sub-task for a specialist profile (with dependencies on earlier steps) or a human-approval
// gate that pauses the run until someone approves (ADR-0044). Passing `edit` loads an existing saved playbook
// and saves in place, keeping its id so schedules stay valid.
export function PlaybookBuilder({
  online,
  edit,
  onSaved,
  onCancel,
}: {
  online: boolean
  edit?: AgentPlaybook
  onSaved: () => void
  onCancel: () => void
}) {
  const [profiles, setProfiles] = useState<AgentProfile[]>([])
  const [name, setName] = useState(edit?.name ?? '')
  const [description, setDescription] = useState(edit?.description ?? '')
  const [goal, setGoal] = useState(edit?.goal ?? '')
  const [steps, setSteps] = useState<Step[]>(
    edit?.steps?.length
      ? edit.steps.map((s) => ({ key: s.key, profile: s.profile, instruction: s.instruction, depends_on: s.depends_on ?? [], gate: !!s.gate }))
      : [{ key: 'step1', profile: '', instruction: '', depends_on: [], gate: false }],
  )
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    void api
      .listAgentProfiles()
      .then((ps) => {
        const specialists = ps.filter((p) => p.id !== 'lead' && p.id !== 'generalist')
        setProfiles(specialists)
        // Only default an empty profile on a brand-new builder, never overwrite a loaded playbook's steps.
        if (!edit) setSteps((s) => s.map((st) => ({ ...st, profile: st.profile || specialists[0]?.id || '' })))
      })
      .catch((e) => setError((e as Error).message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function update(i: number, patch: Partial<Step>) {
    setSteps((s) => s.map((st, j) => (j === i ? { ...st, ...patch } : st)))
  }
  function addStep() {
    setSteps((s) => [...s, { key: `step${s.length + 1}`, profile: profiles[0]?.id ?? '', instruction: '', depends_on: [], gate: false }])
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
      // A gate step carries no profile/instruction — the backend requires them empty.
      const payloadSteps = steps.map((s) =>
        s.gate
          ? { key: s.key, profile: '', instruction: '', depends_on: s.depends_on, gate: true }
          : { key: s.key, profile: s.profile, instruction: s.instruction, depends_on: s.depends_on, gate: false },
      )
      const body = { name: name.trim(), description: description.trim(), goal: goal.trim(), steps: payloadSteps }
      if (edit) await api.updateAgentPlaybook(edit.id, body)
      else await api.createAgentPlaybook(body)
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
        <b>{edit ? 'Edit playbook' : 'New playbook'}</b>
        <span className="grow" />
        <button className="ghost-btn" onClick={onCancel}>Cancel</button>
      </div>
      {error && <div className="banner error">⚠ {error}</div>}
      <input className="pbuild-in" placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} />
      <input className="pbuild-in" placeholder="Goal (what this playbook achieves)" value={goal} onChange={(e) => setGoal(e.target.value)} />
      <input className="pbuild-in" placeholder="Description (optional)" value={description} onChange={(e) => setDescription(e.target.value)} />

      {steps.map((st, i) => (
        <div key={i} className={`pbuild-step ${st.gate ? 'gate' : ''}`}>
          <div className="pbuild-step-h">
            <input className="pbuild-key" placeholder="key" value={st.key} onChange={(e) => update(i, { key: e.target.value })} />
            {st.gate ? (
              <span className="pbuild-gatelbl">⏸ approval gate</span>
            ) : (
              <select value={st.profile} onChange={(e) => update(i, { profile: e.target.value })}>
                {profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            )}
            <label className="pbuild-gate" title="A human-approval pause: the run stops here until someone approves.">
              <input type="checkbox" checked={st.gate} onChange={(e) => update(i, { gate: e.target.checked })} /> gate
            </label>
            <span className="grow" />
            {steps.length > 1 && <button className="orch-del" title="Remove step" onClick={() => removeStep(i)}>×</button>}
          </div>
          {!st.gate && (
            <textarea
              className="pbuild-instr"
              placeholder="Instruction for the specialist — tell it to read existing state first, then do only what's needed."
              value={st.instruction}
              onChange={(e) => update(i, { instruction: e.target.value })}
            />
          )}
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
          {saving ? 'Saving…' : edit ? 'Save changes' : 'Save playbook'}
        </button>
      </div>
    </div>
  )
}
