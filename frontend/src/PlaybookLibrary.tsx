import { useCallback, useEffect, useState } from 'react'
import { api, AgentPlaybook } from './api'
import { PlaybookBuilder } from './PlaybookBuilder'

// PlaybookLibrary is the global authoring surface for agent playbooks (ADR-0019 / IA declutter). Playbook
// definitions are instance-wide (saved_playbooks), so you build and edit them here once and run them from
// any project's Agents surface. Built-ins are shown read-only.
export function PlaybookLibrary({ online }: { online: boolean }) {
  const [playbooks, setPlaybooks] = useState<AgentPlaybook[]>([])
  const [building, setBuilding] = useState(false)
  const [editing, setEditing] = useState<AgentPlaybook | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setPlaybooks(await api.listAgentPlaybooks())
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])
  useEffect(() => {
    if (online) void load()
  }, [online, load])

  async function del(id: string) {
    try {
      await api.deleteAgentPlaybook(id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="lib-section">
      {error && <div className="banner error">⚠ {error}</div>}
      <div className="lib-head">
        <h2>Playbooks</h2>
        <p>Reusable agent playbooks — a DAG of specialist steps with approval gates. Build them once here; run them on a project from its Agents surface.</p>
        <button className="lib-new" disabled={!online} onClick={() => { setEditing(null); setBuilding((v) => !v) }}>
          {building && !editing ? 'Close' : '＋ New playbook'}
        </button>
      </div>

      {(building || editing) && (
        <PlaybookBuilder
          key={editing?.id ?? 'new'}
          online={online}
          edit={editing ?? undefined}
          onCancel={() => { setBuilding(false); setEditing(null) }}
          onSaved={() => {
            setBuilding(false)
            setEditing(null)
            void load()
          }}
        />
      )}

      {playbooks.map((pb) => (
        <div key={pb.id} className="orch-pb">
          <div className="orch-pb-h">
            <b>{pb.name}</b>
            <span className={pb.builtin ? 'orch-builtin' : 'orch-saved'}>{pb.builtin ? 'built-in' : 'saved'}</span>
            <span className="grow" />
            {!pb.builtin && (
              <>
                <button className="orch-edit" title="Edit this playbook" disabled={!online} onClick={() => { setEditing(pb); setBuilding(true) }}>✎ Edit</button>
                <button className="orch-del" title="Delete this playbook" disabled={!online} onClick={() => del(pb.id)}>×</button>
              </>
            )}
          </div>
          <div className="orch-pb-d">{pb.description}</div>
          <div className="orch-pb-steps">
            {(pb.steps ?? []).map((s, i) => (
              <span key={s.key} className="orch-pb-step">
                {i > 0 && <span className="orch-arrow">→</span>}
                <span className={`orch-chip ${s.gate ? 'gate' : ''}`}>{s.gate ? '⏸ gate' : s.profile}</span>
              </span>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}
