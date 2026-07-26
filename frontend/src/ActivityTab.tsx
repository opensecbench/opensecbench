import { useEffect, useState } from 'react'
import { api, ActivityItem, Artifact, Msg, Observation, Plan, PlaybookRun, Task } from './api'

// ActivityTab is the unified, cross-project run history (ADR-0015 successor to the Tasks tab): scanner
// tasks, agent threads, agent plans, and playbook runs interleaved newest-first. Each row opens the
// right kind of detail — a task's artifacts/observations, or an agent thread's full transcript — so a
// run an agent did before a restart stays reviewable rather than vanishing from view.

const KIND_ICON: Record<ActivityItem['kind'], string> = {
  task: '☰',
  thread: '🤖',
  plan: '📋',
  playbook: '📚',
}
const KIND_LABEL: Record<ActivityItem['kind'], string> = {
  task: 'Scan / tool',
  thread: 'Agent',
  plan: 'Plan',
  playbook: 'Playbook',
}

// relTime renders a compact "how long ago" for a run's timestamp.
function relTime(iso: string): string {
  const then = new Date(iso).getTime()
  if (!Number.isFinite(then)) return ''
  const s = Math.max(0, Math.round((Date.now() - then) / 1000))
  if (s < 60) return `${s}s`
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.round(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.round(h / 24)}d`
}

export function ActivityTab({ online, projectId, onError }: { online: boolean; projectId?: string; onError: (m: string) => void }) {
  const [items, setItems] = useState<ActivityItem[]>([])
  const [selected, setSelected] = useState<ActivityItem | null>(null)
  const [kindFilter, setKindFilter] = useState<'all' | ActivityItem['kind']>('all')

  async function load() {
    try {
      setItems((await api.activityFeed(projectId)) ?? [])
    } catch (e) {
      onError((e as Error).message)
    }
  }

  useEffect(() => {
    if (!online) return
    void load()
    const timer = setInterval(load, 3000) // poll so a running item advances to its terminal status
    return () => clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, projectId])

  const shown = kindFilter === 'all' ? items : items.filter((i) => i.kind === kindFilter)

  return (
    <div className="tasks-layout">
      <aside className="task-list">
        <div className="act-filters">
          {(['all', 'task', 'thread', 'plan', 'playbook'] as const).map((k) => (
            <button key={k} className={`act-filter ${kindFilter === k ? 'on' : ''}`} onClick={() => setKindFilter(k)}>
              {k === 'all' ? 'All' : KIND_LABEL[k]}
            </button>
          ))}
        </div>
        {shown.length === 0 && <div className="empty">No activity yet.</div>}
        {shown.map((it) => (
          <button
            key={it.kind + it.id}
            className={`task-row act-row ${selected?.kind === it.kind && selected?.id === it.id ? 'on' : ''}`}
            onClick={() => setSelected(it)}
          >
            <span className={`act-kind k-${it.kind}`} title={KIND_LABEL[it.kind]}>
              {KIND_ICON[it.kind]}
            </span>
            <span className={`badge ${it.status}`}>{it.status}</span>
            <span className="tr-cap">{it.title || '(untitled)'}</span>
            <span className="grow" />
            {it.project && <span className="act-proj">{it.project}</span>}
            <span className="muted">{relTime(it.timestamp)}</span>
          </button>
        ))}
      </aside>

      <div className="task-detail">
        {!selected ? (
          <div className="empty">Select a run to see what it did.</div>
        ) : selected.kind === 'task' ? (
          <TaskDetail item={selected} onError={onError} />
        ) : selected.kind === 'thread' ? (
          <ThreadDetail item={selected} onError={onError} />
        ) : selected.kind === 'plan' ? (
          <PlanDetail item={selected} onError={onError} />
        ) : (
          <PlaybookDetail item={selected} onError={onError} onOpenTask={(t) => setSelected(t)} />
        )}
      </div>
    </div>
  )
}

// --- Task detail (scanner/tool run): artifacts + observations, with cancel and promote-to-finding. ---

function TaskDetail({ item, onError }: { item: ActivityItem; onError: (m: string) => void }) {
  const [task, setTask] = useState<Task | null>(null)
  const [artifacts, setArtifacts] = useState<Artifact[]>([])
  const [observations, setObservations] = useState<Observation[]>([])
  const [obsState, setObsState] = useState<Record<string, string>>({})
  const [content, setContent] = useState<{ name: string; text: string } | null>(null)
  const [findingTitle, setFindingTitle] = useState('')

  async function reload() {
    try {
      const [t, arts, obs] = await Promise.all([
        api.getTask(item.id),
        api.getTaskArtifacts(item.id),
        api.listTaskObservations(item.id),
      ])
      setTask(t)
      setArtifacts(arts ?? [])
      setObservations(obs ?? [])
      const st: Record<string, string> = {}
      for (const o of obs ?? []) st[o.id] = o.review_state
      setObsState(st)
    } catch (e) {
      onError((e as Error).message)
    }
  }

  useEffect(() => {
    setContent(null)
    setFindingTitle('')
    void reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [item.id])

  async function cancel() {
    try {
      await api.cancelTask(item.id)
      await reload()
    } catch (e) {
      onError((e as Error).message)
    }
  }
  async function view(a: Artifact) {
    try {
      setContent({ name: a.name, text: await api.artifactContent(a.id) })
    } catch (e) {
      onError((e as Error).message)
    }
  }
  async function review(o: Observation, state: string) {
    try {
      await api.reviewObservation(o.id, state)
      setObsState((s) => ({ ...s, [o.id]: state }))
    } catch (e) {
      onError((e as Error).message)
    }
  }
  const confirmed = observations.filter((o) => obsState[o.id] === 'confirmed')
  async function promote() {
    if (!findingTitle.trim() || confirmed.length === 0) return
    try {
      await api.createFinding({ title: findingTitle.trim(), severity: 'medium', observation_ids: confirmed.map((o) => o.id) })
      setFindingTitle('')
    } catch (e) {
      onError((e as Error).message)
    }
  }

  const status = task?.status ?? item.status
  return (
    <>
      <section className="panel">
        <div className="panel-head">
          Task <span className={`badge ${status}`}>{status}</span> · {item.title}
          {task?.runner && <> · {task.runner}</>}
          {status === 'running' && (
            <button className="ghost-btn head-right danger" onClick={cancel}>
              ✕ Cancel
            </button>
          )}
        </div>
        {task?.error && <div className="banner error">⚠ {task.error}</div>}
        <div className="rows">
          {artifacts.length === 0 ? (
            <div className="empty">No artifacts (yet).</div>
          ) : (
            artifacts.map((a) => (
              <div key={a.id} className="row-item">
                <span className="badge">{a.kind}</span>
                <span className="row-title">{a.name}</span>
                <span className="muted">{a.media_type} · {a.size} B</span>
                <button className="ghost-btn" onClick={() => view(a)}>
                  View output
                </button>
              </div>
            ))
          )}
        </div>
      </section>

      {content && (
        <section className="panel">
          <div className="panel-head">
            {content.name}
            <button className="ghost-btn head-right" onClick={() => setContent(null)}>
              close
            </button>
          </div>
          <pre className="output">{content.text}</pre>
        </section>
      )}

      <section className="panel">
        <div className="panel-head">Observations ({observations.length})</div>
        {observations.length === 0 ? (
          <div className="empty">No observations from this task.</div>
        ) : (
          <ul className="rows">
            {observations.map((o) => (
              <li key={o.id} className="obs">
                <span className={`sev sev-${o.severity}`}>{o.severity}</span>
                <div className="obs-main">
                  <div className="obs-title">{o.title}</div>
                  <div className="muted mono">{o.rule_id} {o.location}</div>
                </div>
                <div className="obs-actions">
                  <span className={`state state-${obsState[o.id]}`}>{obsState[o.id]}</span>
                  <button className="mini ok" disabled={obsState[o.id] === 'confirmed'} onClick={() => review(o, 'confirmed')}>
                    confirm
                  </button>
                  <button className="mini no" disabled={obsState[o.id] === 'rejected'} onClick={() => review(o, 'rejected')}>
                    reject
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
        {confirmed.length > 0 && (
          <div className="create-row promote">
            <input placeholder="Finding title…" value={findingTitle} onChange={(e) => setFindingTitle(e.target.value)} />
            <button disabled={!findingTitle.trim()} onClick={promote}>
              ⚑ Create finding from {confirmed.length} confirmed
            </button>
          </div>
        )}
      </section>
    </>
  )
}

// --- Thread detail (agent run): the full transcript, read-only — every turn, tool call, and result. ---

function ThreadDetail({ item, onError }: { item: ActivityItem; onError: (m: string) => void }) {
  const [messages, setMessages] = useState<Msg[]>([])
  const [title, setTitle] = useState(item.title)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const d = await api.getThread(item.id)
        if (cancelled) return
        setMessages(d.messages ?? [])
        setTitle(d.thread.title || item.title)
      } catch (e) {
        onError((e as Error).message)
      }
    })()
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [item.id])

  const turns = messages.filter((m) => m.role !== 'system')
  return (
    <section className="panel">
      <div className="panel-head">
        Agent transcript <span className={`badge ${item.status}`}>{item.status}</span> · {title}
        {item.subtitle && <> · {item.subtitle}</>}
      </div>
      {turns.length === 0 ? (
        <div className="empty">No messages in this thread.</div>
      ) : (
        <div className="act-transcript">
          {turns.map((m) => (
            <TranscriptTurn key={m.id} m={m} />
          ))}
        </div>
      )}
    </section>
  )
}

function TranscriptTurn({ m }: { m: Msg }) {
  if (m.role === 'tool') {
    // A tool-result turn (ADR-0017): its content is the tool's output or an error — collapsed by default,
    // expandable so you can read exactly what a tool returned to the agent.
    const first = m.content.split('\n')[0]
    const label = m.content.startsWith('Tool ') ? first : 'tool result'
    return (
      <details className={`act-msg tool${m.tool_error ? ' error' : ''}`}>
        <summary>🔧 {label}</summary>
        <pre className="act-toolout">{m.content}</pre>
      </details>
    )
  }
  if (m.role === 'assistant') {
    if (m.tool_calls && m.tool_calls.length > 0) {
      const c = m.tool_calls[0]
      return (
        <details className="act-msg propose">
          <summary>⚙ ran <b>{c.tool}</b></summary>
          <pre className="act-toolout">{JSON.stringify(c.args ?? {}, null, 2)}</pre>
        </details>
      )
    }
    return (
      <div className="act-msg analyst">
        <b>Agent</b>
        <div className="act-msg-body">{m.content}</div>
      </div>
    )
  }
  return (
    <div className="act-msg user">
      <b>You</b>
      <div className="act-msg-body">{m.content}</div>
    </div>
  )
}

// --- Plan detail (agent DAG run): each step's status, result, and live progress trail. ---

function PlanDetail({ item, onError }: { item: ActivityItem; onError: (m: string) => void }) {
  const [plan, setPlan] = useState<Plan | null>(null)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const p = await api.getPlan(item.id)
        if (!cancelled) setPlan(p)
      } catch (e) {
        onError((e as Error).message)
      }
    })()
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [item.id])

  const steps = plan?.steps ?? []
  return (
    <section className="panel">
      <div className="panel-head">
        Plan <span className={`badge ${item.status}`}>{item.status}</span> · {plan?.goal || item.title}
      </div>
      {steps.length === 0 ? (
        <div className="empty">No steps recorded.</div>
      ) : (
        <div className="act-steps">
          {steps.map((s) => (
            <details key={s.id} className="act-step">
              <summary>
                <span className={`badge ${s.status}`}>{s.status}</span>
                <span className="act-step-key">{s.key}</span>
                <span className="muted">{s.profile}</span>
              </summary>
              {s.instruction && <div className="act-step-instr">{s.instruction}</div>}
              {s.result && <pre className="act-toolout">{s.result}</pre>}
              {s.error && <div className="banner error">⚠ {s.error}</div>}
              {s.progress && <pre className="act-toolout muted">{s.progress}</pre>}
            </details>
          ))}
        </div>
      )}
    </section>
  )
}

// --- Playbook run detail: the child capability tasks it enqueued, each drillable into its task view. ---

function PlaybookDetail({
  item,
  onError,
  onOpenTask,
}: {
  item: ActivityItem
  onError: (m: string) => void
  onOpenTask: (t: ActivityItem) => void
}) {
  const [run, setRun] = useState<PlaybookRun | null>(null)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const r = await api.getPlaybookRun(item.id)
        if (!cancelled) setRun(r)
      } catch (e) {
        onError((e as Error).message)
      }
    })()
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [item.id])

  const taskIds = run?.task_ids ?? []
  return (
    <section className="panel">
      <div className="panel-head">
        Playbook run <span className={`badge ${item.status}`}>{item.status}</span> · {item.title}
      </div>
      {taskIds.length === 0 ? (
        <div className="empty">No tasks in this run.</div>
      ) : (
        <div className="rows">
          {taskIds.map((id, i) => (
            <button
              key={id}
              className="row-item act-child"
              onClick={() =>
                onOpenTask({ kind: 'task', id, title: `Step ${i + 1}`, status: item.status, timestamp: item.timestamp, project_id: item.project_id, project: item.project })
              }
            >
              <span className="badge">task</span>
              <span className="row-title mono">{id.slice(0, 8)}</span>
              <span className="muted">open →</span>
            </button>
          ))}
        </div>
      )}
    </section>
  )
}
