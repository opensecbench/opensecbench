import { useEffect, useState } from 'react'
import { api, Artifact, Observation, Task } from './api'

export function TasksTab({ online, onError }: { online: boolean; onError: (m: string) => void }) {
  const [tasks, setTasks] = useState<Task[]>([])
  const [selected, setSelected] = useState<Task | null>(null)
  const [artifacts, setArtifacts] = useState<Artifact[]>([])
  const [observations, setObservations] = useState<Observation[]>([])
  const [obsState, setObsState] = useState<Record<string, string>>({})
  const [content, setContent] = useState<{ name: string; text: string } | null>(null)
  const [findingTitle, setFindingTitle] = useState('')

  async function loadTasks() {
    try {
      setTasks((await api.listTasks()) ?? [])
    } catch (e) {
      onError((e as Error).message)
    }
  }

  useEffect(() => {
    if (!online) return
    void loadTasks()
    const timer = setInterval(loadTasks, 3000) // poll so a running task updates when it finishes
    return () => clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  async function openTask(t: Task) {
    setSelected(t)
    setContent(null)
    setFindingTitle('')
    try {
      const [arts, obs] = await Promise.all([api.getTaskArtifacts(t.id), api.listTaskObservations(t.id)])
      setArtifacts(arts ?? [])
      setObservations(obs ?? [])
      const st: Record<string, string> = {}
      for (const o of obs ?? []) st[o.id] = o.review_state
      setObsState(st)
    } catch (e) {
      onError((e as Error).message)
    }
  }

  async function cancel(t: Task) {
    try {
      await api.cancelTask(t.id)
      const list = (await api.listTasks()) ?? []
      setTasks(list)
      const updated = list.find((x) => x.id === t.id)
      if (updated) setSelected(updated)
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

  return (
    <div className="tasks-layout">
      <aside className="task-list">
        {tasks.length === 0 && <div className="empty">No tasks yet.</div>}
        {tasks.map((t) => (
          <button key={t.id} className={`task-row ${selected?.id === t.id ? 'on' : ''}`} onClick={() => openTask(t)}>
            <span className={`badge ${t.status}`}>{t.status}</span>
            <span className="tr-cap">{t.capability_id}</span>
            <span className="muted">{new Date(t.created_at).toLocaleTimeString()}</span>
          </button>
        ))}
      </aside>

      <div className="task-detail">
        {!selected ? (
          <div className="empty">Select a task to see its captured output.</div>
        ) : (
          <>
            <section className="panel">
              <div className="panel-head">
                Task <span className={`badge ${selected.status}`}>{selected.status}</span> · {selected.capability_id} · {selected.runner}
                {selected.status === 'running' && (
                  <button className="ghost-btn head-right danger" onClick={() => cancel(selected)}>
                    ✕ Cancel
                  </button>
                )}
              </div>
              {selected.error && <div className="banner error">⚠ {selected.error}</div>}
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
        )}
      </div>
    </div>
  )
}
