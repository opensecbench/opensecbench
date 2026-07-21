import { useCallback, useEffect, useState } from 'react'
import { api, AgentPlaybook, Plan, Project, Schedule } from './api'

const DAY = 86400
const WEEK = 604800

function cadence(seconds: number): string {
  if (seconds === DAY) return 'daily'
  if (seconds === WEEK) return 'weekly'
  if (seconds % DAY === 0) return `every ${seconds / DAY}d`
  return `every ${Math.round(seconds / 3600)}h`
}

// Activity is one turn of a step's live trail: the agent's commentary plus the tool it ran. The backend
// streams these as JSON lines (see formatActivity); older runs may carry plain-text lines, kept as `raw`.
type Activity = { k: string; tool: string; note?: string; args?: string; out?: string; raw?: string }

function parseActivity(progress: string): Activity[] {
  return progress
    .trimEnd()
    .split('\n')
    .filter((l) => l.trim() !== '')
    .map((line) => {
      if (line.startsWith('{')) {
        try {
          const e = JSON.parse(line)
          return { k: e.k ?? 'ok', tool: e.tool ?? '', note: e.note, args: e.args, out: e.out }
        } catch {
          /* not JSON — fall through to raw */
        }
      }
      return { k: 'ok', tool: '', raw: line }
    })
}

// ActivityEntry leads with the agent's own words (what it's thinking/doing) and tucks the mechanical tool
// call — name, args, output — behind a details toggle, so the trail reads like reasoning, not a log dump.
function ActivityEntry({ e }: { e: Activity }) {
  const [open, setOpen] = useState(false)
  if (e.raw) return <div className="act-entry act-raw">{e.raw}</div>
  const glyph = e.k === 'err' ? '✗' : e.k === 'deny' ? '⏸' : '→'
  const hasDetail = !!(e.args || e.out)
  return (
    <div className={`act-entry k-${e.k}`}>
      {e.note && <div className="act-note">{e.note}</div>}
      <div className="act-tool">
        <span className="act-glyph">{glyph}</span>
        <code className="act-toolname">{e.tool}</code>
        {e.k === 'deny' && <span className="act-tag">denied</span>}
        {hasDetail && (
          <button type="button" className="act-toggle" onClick={() => setOpen(!open)}>
            {open ? 'hide' : 'details'}
          </button>
        )}
      </div>
      {open && hasDetail && (
        <div className="act-detail">
          {e.args && (
            <pre className="act-code">
              <span className="act-lbl">args</span>
              {e.args}
            </pre>
          )}
          {e.out && (
            <pre className="act-code">
              <span className="act-lbl">{e.k === 'err' ? 'error' : 'result'}</span>
              {e.out}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}

// OrchestrateTab runs and schedules agent playbooks on this project, and watches the resulting plan (a DAG
// of specialist steps) run to completion (ADR-0019). Playbooks are authored in the global Library; here you
// pick one to run or schedule.
export function OrchestrateTab({ project, online, onError }: { project: Project; online: boolean; onError: (m: string) => void }) {
  const [playbooks, setPlaybooks] = useState<AgentPlaybook[]>([])
  const [plans, setPlans] = useState<Plan[]>([])
  const [selected, setSelected] = useState<Plan | null>(null)
  const [schedules, setSchedules] = useState<Schedule[]>([])
  const [busy, setBusy] = useState('')

  const loadPlaybooks = useCallback(async () => {
    try {
      setPlaybooks(await api.listAgentPlaybooks())
    } catch (e) {
      onError((e as Error).message)
    }
  }, [onError])

  const loadSchedules = useCallback(async () => {
    try {
      setSchedules((await api.listSchedules(project.id)) ?? [])
    } catch (e) {
      onError((e as Error).message)
    }
  }, [project.id, onError])

  useEffect(() => {
    if (online) {
      void loadPlaybooks()
      void loadSchedules()
    }
  }, [online, loadPlaybooks, loadSchedules])

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

  async function stop(p: Plan) {
    try {
      setSelected(await api.cancelPlan(p.id))
      await loadPlans()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  async function schedule(pb: AgentPlaybook, seconds: number) {
    try {
      await api.createSchedule(project.id, pb.id, seconds)
      await loadSchedules()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  async function toggleSchedule(sc: Schedule) {
    try {
      await api.setScheduleEnabled(sc.id, !sc.enabled)
      await loadSchedules()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  async function removeSchedule(id: string) {
    try {
      await api.deleteSchedule(id)
      await loadSchedules()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  const playbookName = (id: string) => playbooks.find((p) => p.id === id)?.name ?? id

  async function saveAsPlaybook(plan: Plan) {
    const name = window.prompt('Save this run as a reusable playbook. Name:')
    if (!name) return
    try {
      await api.savePlanAsPlaybook(plan.id, name, `Recorded from a ${plan.playbook_id} run.`)
      await loadPlaybooks()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  // A selected/running plan takes over the whole tab (focus mode); otherwise the tab browses playbooks,
  // schedules, and prior runs at full width. Running a playbook sets `selected`, so it auto-focuses.
  if (selected) {
    return (
      <div className="orch-tab">
        <div className="orch-focus">
          <div className="plan-view">
            <div className="plan-h">
              <button className="orch-back" onClick={() => setSelected(null)} title="Back to playbooks">← Playbooks</button>
              <span className={`orch-dot s-${selected.status}`} />
              <b>{selected.playbook_id}</b>
              <span className="plan-goal">{selected.goal}</span>
              <span className="grow" />
              {(selected.status === 'running' || selected.status === 'waiting') && (
                <button className="orch-stop" disabled={!online} onClick={() => stop(selected)} title="Stop this run — aborts in-flight work and skips remaining steps">
                  ■ Stop
                </button>
              )}
              <button className="ghost-btn" disabled={!online} onClick={() => saveAsPlaybook(selected)} title="Record this run as a reusable playbook">
                ＋ Save as playbook
              </button>
            </div>
            {(selected.steps ?? []).map((s) => (
              <div key={s.id} className={`plan-step st-${s.status}`}>
                <div className="ps-head">
                  <span className={`orch-dot s-${s.status}`} />
                  <b>{s.key}</b>
                  <span className="ps-profile">{s.profile}</span>
                  {(s.depends_on ?? []).length > 0 && <span className="ps-dep">after {(s.depends_on ?? []).join(', ')}</span>}
                  <span className="grow" />
                  <span className={`ps-status s-${s.status}`}>{s.status}</span>
                </div>
                {s.progress && (() => {
                  const entries = parseActivity(s.progress)
                  return (
                    <details className="ps-activity" open={s.status === 'running'}>
                      <summary>
                        Activity <span className="ps-activity-n">{entries.length} turns</span>
                      </summary>
                      <div className="ps-activity-log">
                        {entries.map((e, i) => (
                          <ActivityEntry key={i} e={e} />
                        ))}
                      </div>
                    </details>
                  )
                })()}
                {s.result && <div className="ps-result">{s.result}</div>}
                {s.error && <div className="ps-error">{s.error}</div>}
              </div>
            ))}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="orch-tab">
      <div className="orch-browse">
        <div className="orch-section-h orch-ph">
          <span>Playbooks</span>
        </div>
        {playbooks.length === 0 && <div className="orch-empty">No playbooks yet — build one in the Library.</div>}
        <div className="orch-pb-grid">
          {playbooks.map((pb) => (
            <div key={pb.id} className="orch-pb">
              <div className="orch-pb-h">
                <b>{pb.name}</b>
                {!pb.builtin && <span className="orch-saved">saved</span>}
                <span className="grow" />
                <button disabled={!online || !!busy} onClick={() => run(pb)}>{busy === pb.id ? '…' : '▷ Run'}</button>
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
              <div className="orch-sched-add">
                <span>Schedule</span>
                <button disabled={!online} onClick={() => schedule(pb, DAY)}>Daily</button>
                <button disabled={!online} onClick={() => schedule(pb, WEEK)}>Weekly</button>
              </div>
            </div>
          ))}
        </div>

        {schedules.length > 0 && (
          <>
            <div className="orch-section-h">Schedules</div>
            <div className="orch-sched-grid">
              {schedules.map((sc) => (
                <div key={sc.id} className={`orch-sched ${sc.enabled ? '' : 'off'}`}>
                  <span className="orch-sched-name">{playbookName(sc.playbook_id)}</span>
                  <span className="orch-sched-cadence">{cadence(sc.interval_seconds)}</span>
                  <span className="grow" />
                  <button className="orch-sched-btn" onClick={() => toggleSchedule(sc)} title={sc.enabled ? 'Pause' : 'Resume'}>
                    {sc.enabled ? '⏸' : '▷'}
                  </button>
                  <button className="orch-del" onClick={() => removeSchedule(sc.id)} title="Delete schedule">×</button>
                </div>
              ))}
            </div>
          </>
        )}

        <div className="orch-section-h">Runs</div>
        {plans.length === 0 && <div className="orch-empty">No runs yet — trigger a playbook above.</div>}
        <div className="orch-runs-grid">
          {plans.map((p) => (
            <button key={p.id} className="orch-run" onClick={() => openPlan(p)}>
              <span className={`orch-dot s-${p.status}`} />
              <span className="orch-run-name">{p.playbook_id}</span>
              <span className="orch-run-time">{new Date(p.created_at).toLocaleTimeString()}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}
