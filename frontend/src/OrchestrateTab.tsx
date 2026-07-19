import { useCallback, useEffect, useState } from 'react'
import { api, AgentPlaybook, Plan, Project } from './api'

// OrchestrateTab lets a human trigger an agent playbook and watch the resulting plan (a DAG of steps,
// each delegated to a specialist) run to completion (ADR-0019).
export function OrchestrateTab({ project, online, onError }: { project: Project; online: boolean; onError: (m: string) => void }) {
  const [playbooks, setPlaybooks] = useState<AgentPlaybook[]>([])
  const [plans, setPlans] = useState<Plan[]>([])
  const [selected, setSelected] = useState<Plan | null>(null)
  const [busy, setBusy] = useState('')

  useEffect(() => {
    if (online) void api.listAgentPlaybooks().then(setPlaybooks).catch((e) => onError((e as Error).message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  const loadPlans = useCallback(async () => {
    try {
      setPlans((await api.listPlans(project.id)) ?? [])
    } catch (e) {
      onError((e as Error).message)
    }
  }, [project.id, onError])

  useEffect(() => {
    if (!online) return
    void loadPlans()
    const t = setInterval(loadPlans, 3000)
    return () => clearInterval(t)
  }, [online, loadPlans])

  // Poll the open plan while it is running so steps light up as they finish.
  useEffect(() => {
    if (!selected || selected.status !== 'running') return
    const t = setInterval(async () => {
      try {
        setSelected(await api.getPlan(selected.id))
      } catch {
        /* transient */
      }
    }, 2000)
    return () => clearInterval(t)
  }, [selected])

  async function run(pb: AgentPlaybook) {
    setBusy(pb.id)
    try {
      const plan = await api.startPlan(project.id, pb.id)
      await loadPlans()
      setSelected(await api.getPlan(plan.id))
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  async function openPlan(p: Plan) {
    try {
      setSelected(await api.getPlan(p.id))
    } catch (e) {
      onError((e as Error).message)
    }
  }

  return (
    <div className="orch-tab">
      <div className="orch-left">
        <div className="orch-section-h">Playbooks</div>
        {playbooks.map((pb) => (
          <div key={pb.id} className="orch-pb">
            <div className="orch-pb-h">
              <b>{pb.name}</b>
              <button disabled={!online || !!busy} onClick={() => run(pb)}>{busy === pb.id ? '…' : '▷ Run'}</button>
            </div>
            <div className="orch-pb-d">{pb.description}</div>
            <div className="orch-pb-steps">
              {pb.steps.map((s, i) => (
                <span key={s.key} className="orch-pb-step">
                  {i > 0 && <span className="orch-arrow">→</span>}
                  <span className="orch-chip">{s.profile}</span>
                </span>
              ))}
            </div>
          </div>
        ))}

        <div className="orch-section-h">Runs</div>
        {plans.length === 0 && <div className="orch-empty">No runs yet — trigger a playbook above.</div>}
        {plans.map((p) => (
          <button key={p.id} className={`orch-run ${selected?.id === p.id ? 'on' : ''}`} onClick={() => openPlan(p)}>
            <span className={`orch-dot s-${p.status}`} />
            <span className="orch-run-name">{p.playbook_id}</span>
            <span className="orch-run-time">{new Date(p.created_at).toLocaleTimeString()}</span>
          </button>
        ))}
      </div>

      <div className="orch-right">
        {!selected ? (
          <div className="empty">Run a playbook, or pick a run to watch its plan.</div>
        ) : (
          <div className="plan-view">
            <div className="plan-h">
              <span className={`orch-dot s-${selected.status}`} />
              <b>{selected.playbook_id}</b>
              <span className="plan-goal">{selected.goal}</span>
            </div>
            {(selected.steps ?? []).map((s) => (
              <div key={s.id} className={`plan-step st-${s.status}`}>
                <div className="ps-head">
                  <span className={`orch-dot s-${s.status}`} />
                  <b>{s.key}</b>
                  <span className="ps-profile">{s.profile}</span>
                  {s.depends_on.length > 0 && <span className="ps-dep">after {s.depends_on.join(', ')}</span>}
                  <span className="grow" />
                  <span className={`ps-status s-${s.status}`}>{s.status}</span>
                </div>
                {s.result && <div className="ps-result">{s.result}</div>}
                {s.error && <div className="ps-error">{s.error}</div>}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
