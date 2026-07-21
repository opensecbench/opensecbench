import { useEffect, useRef, useState } from 'react'
import { api, Plan, Task } from './api'

// ActivityMenu is the top-bar "what's running" indicator: a live count of in-flight capability tasks and
// agent plans across the workbench, expandable to a list. Polls while online so it reflects a scan you
// just kicked off.
export function ActivityMenu({ online }: { online: boolean }) {
  const [tasks, setTasks] = useState<Task[]>([])
  const [plans, setPlans] = useState<Plan[]>([])
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  async function reload() {
    try {
      const a = await api.activity()
      setTasks(a.tasks ?? [])
      setPlans(a.plans ?? [])
    } catch {
      /* offline; leave as-is */
    }
  }

  useEffect(() => {
    if (!online) return
    void reload()
    const timer = setInterval(reload, 3000)
    return () => clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const total = tasks.length + plans.length
  const running = total > 0
  return (
    <div className="activity" ref={ref}>
      <button className={`activity-btn ${running ? 'busy' : ''}`} onClick={() => setOpen((o) => !o)} title="What's running">
        <span className={`activity-spin ${running ? 'on' : ''}`}>◍</span>
        {running ? total : 'idle'}
      </button>
      {open && (
        <div className="activity-panel">
          <div className="activity-head">Running now</div>
          {total === 0 ? (
            <div className="empty">Nothing running.</div>
          ) : (
            <>
              {tasks.length > 0 && (
                <>
                  <div className="activity-group">Scans &amp; tools ({tasks.length})</div>
                  <ul>
                    {tasks.map((t) => (
                      <li key={t.id}>
                        <span className={`act-dot s-${t.status}`} />
                        <span className="act-name mono">{t.capability_id}</span>
                        <span className="act-status">{t.status}</span>
                      </li>
                    ))}
                  </ul>
                </>
              )}
              {plans.length > 0 && (
                <>
                  <div className="activity-group">Agents ({plans.length})</div>
                  <ul>
                    {plans.map((p) => (
                      <li key={p.id}>
                        <span className={`act-dot s-${p.status}`} />
                        <span className="act-name mono">{p.playbook_id}</span>
                        <span className="act-status">{p.status}</span>
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}
