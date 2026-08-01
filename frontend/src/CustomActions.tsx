import { useEffect, useMemo, useState } from 'react'
import { api, Action, AgentProfile } from './api'
import { actionIcon } from './customActions'

// CustomActions is the Library authoring surface for custom actions (ADR-0059): user-defined operations —
// an LLM agent or a sandboxed script — that run against a finding or observation, templated from its
// fields. Built-in examples are shown read-only and can be cloned into an editable copy. Mirrors the
// CustomAgents editor.

const TECHNIQUES = ['', 'intrusive', 'automated_exploit', 'brute_force', 'dos', 'social', 'destructive']
const SEVERITIES = ['', 'info', 'low', 'medium', 'high', 'critical']
const SUBJECT_TOKENS = ['{{subject.title}}', '{{subject.severity}}', '{{subject.cwe}}', '{{subject.location}}', '{{subject.description}}', '{{subject.status}}', '{{project.environment}}']

type Draft = {
  name: string
  description: string
  icon: string
  kind: 'agent' | 'script'
  subjectFinding: boolean
  subjectObservation: boolean
  minSeverity: string
  technique: string
  profileId: string
  instruction: string
  image: string
  command: string
  recordObservations: boolean
  writeToPath: string
}

const EMPTY: Draft = {
  name: '', description: '', icon: '', kind: 'agent',
  subjectFinding: true, subjectObservation: true,
  minSeverity: '', technique: '', profileId: '', instruction: '',
  image: 'alpine:3', command: '', recordObservations: false, writeToPath: '',
}

// Derive an editable command string from a stored argv (we store `sh -c "<command>"` by default).
function commandFromCmd(cmd?: string[]): string {
  if (!cmd || cmd.length === 0) return ''
  if (cmd.length === 3 && cmd[0] === 'sh' && cmd[1] === '-c') return cmd[2]
  return cmd.join(' ')
}

function draftFrom(a: Action): Draft {
  return {
    name: a.name, description: a.description ?? '', icon: a.icon ?? '', kind: a.kind,
    subjectFinding: a.subject_kinds?.includes('finding') ?? false,
    subjectObservation: a.subject_kinds?.includes('observation') ?? false,
    minSeverity: a.applies_when?.min_severity ?? '', technique: a.technique ?? '',
    profileId: a.profile_id ?? '', instruction: a.instruction ?? '',
    image: a.image || 'alpine:3', command: commandFromCmd(a.cmd),
    recordObservations: a.output?.record_observations ?? false, writeToPath: a.output?.write_to_path ?? '',
  }
}

function toPayload(d: Draft): Partial<Action> {
  const subject_kinds = [d.subjectFinding && 'finding', d.subjectObservation && 'observation'].filter(Boolean) as string[]
  const base: Partial<Action> = {
    name: d.name.trim(), description: d.description.trim(), icon: d.icon.trim(), kind: d.kind,
    subject_kinds, applies_when: d.minSeverity ? { min_severity: d.minSeverity } : {},
    technique: d.technique, output: { record_observations: d.kind === 'agent' && d.recordObservations, write_to_path: d.writeToPath.trim() },
  }
  if (d.kind === 'agent') {
    base.profile_id = d.profileId
    base.instruction = d.instruction.trim()
  } else {
    base.image = d.image.trim()
    base.cmd = ['sh', '-c', d.command]
  }
  return base
}

export function CustomActions({ online }: { online: boolean }) {
  const [actions, setActions] = useState<Action[]>([])
  const [profiles, setProfiles] = useState<AgentProfile[]>([])
  const [editing, setEditing] = useState<{ id: string | null; draft: Draft } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  async function load() {
    try {
      const [as, ps] = await Promise.all([api.listActions(), api.listAgentProfiles()])
      setActions(as)
      setProfiles(ps)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    if (online) void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  const builtins = actions.filter((a) => a.builtin)
  const saved = actions.filter((a) => !a.builtin)
  const d = editing?.draft
  const valid = useMemo(() => {
    if (!d) return false
    if (!d.name.trim() || (!d.subjectFinding && !d.subjectObservation)) return false
    return d.kind === 'agent' ? !!(d.profileId && d.instruction.trim()) : !!(d.image.trim() && d.command.trim())
  }, [d])

  function set<K extends keyof Draft>(k: K, v: Draft[K]) {
    setEditing((e) => (e ? { ...e, draft: { ...e.draft, [k]: v } } : e))
  }

  async function save() {
    if (!editing || !d) return
    setError(null)
    setSaving(true)
    try {
      if (editing.id) await api.updateAction(editing.id, toPayload(d))
      else await api.createAction(toPayload(d))
      setEditing(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  async function remove(id: string) {
    try {
      await api.deleteAction(id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="agents">
      <div className="prov-add-title agents-h">
        <span>Custom actions</span>
        <span className="grow" />
        <button className="orch-new" disabled={!online} onClick={() => setEditing({ id: null, draft: { ...EMPTY } })}>＋ New</button>
      </div>
      <p className="agents-sub">Operations you run on a finding or observation — an LLM agent or a sandboxed script, templated from the subject. Clone an example to tailor it to your environment.</p>
      {error && <div className="banner error">⚠ {error}</div>}

      {builtins.length > 0 && (
        <>
          <div className="agents-grp">Examples (clone to customize)</div>
          {builtins.map((a) => (
            <div key={a.id} className="agents-row">
              <div className="act-ico">{actionIcon(a)}</div>
              <div>
                <div className="agents-name">{a.name} <span className={`act-kind ${a.kind}`}>{a.kind}</span></div>
                <div className="agents-tools">{a.description}</div>
              </div>
              <span className="grow" />
              <button className="orch-new" onClick={() => setEditing({ id: null, draft: { ...draftFrom(a), name: a.name + ' (copy)' } })}>Clone</button>
            </div>
          ))}
        </>
      )}

      <div className="agents-grp">Your actions</div>
      {saved.length === 0 && <div className="agents-empty">No custom actions yet. Clone an example above or start fresh with ＋ New.</div>}
      {saved.map((a) => (
        <div key={a.id} className="agents-row">
          <div className="act-ico">{actionIcon(a)}</div>
          <div>
            <div className="agents-name">{a.name} <span className={`act-kind ${a.kind}`}>{a.kind}</span></div>
            <div className="agents-tools">{a.description || (a.subject_kinds ?? []).join(', ')}</div>
          </div>
          <span className="grow" />
          <button className="orch-new" onClick={() => setEditing({ id: a.id, draft: draftFrom(a) })}>Edit</button>
          <button className="orch-del" title="Delete" onClick={() => remove(a.id)}>×</button>
        </div>
      ))}

      {editing && d && (
        <div className="pbuild act-editor">
          <div className="act-grid">
            <input className="pbuild-in" placeholder="Name (e.g. Hunt logs for abuse)" value={d.name} onChange={(e) => set('name', e.target.value)} />
            <input className="pbuild-in act-icon" placeholder="Icon (emoji)" value={d.icon} onChange={(e) => set('icon', e.target.value)} />
          </div>
          <input className="pbuild-in" placeholder="Description — what this does, one line" value={d.description} onChange={(e) => set('description', e.target.value)} />

          <div className="agents-tools-label">Kind</div>
          <div className="act-seg">
            <button className={d.kind === 'agent' ? 'on' : ''} onClick={() => set('kind', 'agent')}>✦ LLM agent</button>
            <button className={d.kind === 'script' ? 'on' : ''} onClick={() => set('kind', 'script')}>›_ Sandboxed script</button>
          </div>

          <div className="agents-tools-label">Runs on</div>
          <div className="act-checks">
            <label className={d.subjectFinding ? 'on' : ''}><input type="checkbox" checked={d.subjectFinding} onChange={(e) => set('subjectFinding', e.target.checked)} /> Findings</label>
            <label className={d.subjectObservation ? 'on' : ''}><input type="checkbox" checked={d.subjectObservation} onChange={(e) => set('subjectObservation', e.target.checked)} /> Observations</label>
          </div>

          {d.kind === 'agent' ? (
            <>
              <div className="agents-tools-label">Agent profile</div>
              <select className="pbuild-in" value={d.profileId} onChange={(e) => set('profileId', e.target.value)}>
                <option value="">Choose a profile…</option>
                {profiles.map((p) => <option key={p.id} value={p.id}>{p.name}{p.builtin ? '' : ' (custom)'}</option>)}
              </select>
              <div className="agents-tools-label">Instruction template</div>
              <textarea className="pbuild-instr" placeholder="What the agent should do. Use {{subject.title}}, {{subject.location}}, …" value={d.instruction} onChange={(e) => set('instruction', e.target.value)} />
              <label className="act-inline"><input type="checkbox" checked={d.recordObservations} onChange={(e) => set('recordObservations', e.target.checked)} /> Record hits as observations (feeds triage)</label>
            </>
          ) : (
            <>
              <div className="agents-tools-label">Container image</div>
              <input className="pbuild-in" placeholder="e.g. alpine:3 or your-registry/siem-cli:1" value={d.image} onChange={(e) => set('image', e.target.value)} />
              <div className="agents-tools-label">Command (runs as sh -c; workspace mounted at /work)</div>
              <textarea className="pbuild-instr act-mono" placeholder='e.g. siem-cli search "$OSB_SUBJECT_TITLE" --since 30d' value={d.command} onChange={(e) => set('command', e.target.value)} />
              <div className="act-hint">Subject values arrive as env vars — OSB_SUBJECT_TITLE, OSB_SUBJECT_LOCATION, OSB_SUBJECT_SEVERITY, OSB_SUBJECT_CWE, … — never shell-interpolated.</div>
            </>
          )}

          <div className="act-tokens">
            {SUBJECT_TOKENS.map((t) => <span key={t} className="act-tok">{t}</span>)}
          </div>

          <div className="act-grid">
            <div>
              <div className="agents-tools-label">Applies when severity ≥</div>
              <select className="pbuild-in" value={d.minSeverity} onChange={(e) => set('minSeverity', e.target.value)}>
                {SEVERITIES.map((s) => <option key={s} value={s}>{s || 'any'}</option>)}
              </select>
            </div>
            <div>
              <div className="agents-tools-label">Consequence tier (ROE)</div>
              <select className="pbuild-in" value={d.technique} onChange={(e) => set('technique', e.target.value)}>
                {TECHNIQUES.map((t) => <option key={t} value={t}>{t || 'passive (always allowed)'}</option>)}
              </select>
            </div>
          </div>
          {d.technique && <div className="act-gate">⚠ Tagged <b>{d.technique}</b> — runs only when the project's engagement authorizes that technique.</div>}

          <div className="pbuild-actions">
            <button className="link" onClick={() => setEditing(null)}>Cancel</button>
            <span className="grow" />
            <button className="pbuild-save" disabled={!online || saving || !valid} onClick={save}>
              {saving ? 'Saving…' : editing.id ? 'Save changes' : 'Save action'}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
