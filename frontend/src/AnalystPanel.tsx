import { useEffect, useRef, useState, type FormEvent, type MouseEvent as ReactMouseEvent } from 'react'
import { api, ActiveProvider, AgentProfile, Approval, Msg, Project, StreamDelta, StreamMessage, Thread } from './api'
import { Markdown } from './Markdown'
import { ApprovalCard } from './ApprovalCard'

export function AnalystPanel({
  project,
  online,
  initialThread,
  drive,
  onDriveChange,
  getView,
}: {
  project: Project
  online: boolean
  initialThread?: string
  // Co-driving: when Drive is on, the Analyst's "show" tool moves the workbench for you (ADR-0053). Owned by
  // the Workbench (which applies the navigation); this panel just renders the toggle.
  drive?: boolean
  onDriveChange?: (v: boolean) => void
  // getView returns a short description of what's on screen right now, sent with each message so the Analyst
  // can resolve "explain this" to the on-screen finding/code/surface (ADR-0053 awareness).
  getView?: () => string
}) {
  const [threads, setThreads] = useState<Thread[]>([])
  const [current, setCurrent] = useState<Thread | null>(null)
  const [messages, setMessages] = useState<Msg[]>([])
  const [pending, setPending] = useState<Approval | null>(null)
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [collapsed, setCollapsed] = useState(false)
  const [provider, setProvider] = useState<ActiveProvider | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [profiles, setProfiles] = useState<AgentProfile[]>([])
  const [profileId, setProfileId] = useState('generalist')
  // Autonomy envelope (ADR-0054): how much the Analyst does without asking. The control surface for the
  // consequence-tier governance — a global setting, surfaced here as a quick header control.
  const [autonomy, setAutonomyState] = useState('cautious')
  useEffect(() => {
    if (online) void api.getApprovalPolicy().then((p) => setAutonomyState(p.autonomy ?? 'cautious')).catch(() => {})
  }, [online])
  async function changeAutonomy(v: string) {
    setAutonomyState(v) // optimistic
    try {
      await api.setAutonomy(v)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  // Live turns (ADR-0053): while a turn runs, its steps stream in over the event bus. Refs keep the SSE
  // handler current without resubscribing — it only appends while a send is in flight (streaming) and only
  // for the open thread; the authoritative refresh() at turn end replaces these temp-keyed messages.
  const currentRef = useRef<Thread | null>(null)
  currentRef.current = current
  const streamingRef = useRef(false)
  const streamKey = useRef(0)
  // streamingText is the assistant's answer as it types out (token deltas). Rendered as a transient bubble
  // until the completed message for that turn arrives, which finalizes it into a real message and clears this.
  const [streamingText, setStreamingText] = useState('')

  // Resizable panel: drag the left edge to widen for reading a verbose answer, shrink back to reclaim the
  // workbench — so the chat never has to take over the screen. Persisted.
  const [panelWidth, setPanelWidth] = useState<number>(() => {
    const v = Number(localStorage.getItem('osb.analyst.width'))
    return v >= 320 && v <= 900 ? v : 400
  })
  useEffect(() => {
    try {
      localStorage.setItem('osb.analyst.width', String(panelWidth))
    } catch {
      /* ignore */
    }
  }, [panelWidth])
  function startResize(e: ReactMouseEvent) {
    e.preventDefault()
    const startX = e.clientX
    const startW = panelWidth
    const onMove = (ev: MouseEvent) => setPanelWidth(Math.min(900, Math.max(320, startW + (startX - ev.clientX))))
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      document.body.style.userSelect = ''
    }
    document.body.style.userSelect = 'none'
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }
  useEffect(() => {
    if (!online) return
    return api.subscribeProjectEvents(project.id, {
      analystDelta: (d: StreamDelta) => {
        if (!streamingRef.current || d.thread_id !== currentRef.current?.id) return
        setStreamingText((t) => t + d.text)
      },
      analystMessage: (m: StreamMessage) => {
        if (!streamingRef.current || m.thread_id !== currentRef.current?.id) return
        // This turn's text is now a finalized message — drop the live-typing bubble it was building.
        if (m.role === 'assistant') setStreamingText('')
        setMessages((ms) => [
          ...ms,
          {
            id: `stream:${streamKey.current++}`,
            thread_id: m.thread_id,
            seq: ms.length,
            role: m.role,
            content: m.content,
            tool_calls: m.tool_calls,
            tool_call_id: m.tool_call_id,
            tool_error: m.tool_error,
            created_at: '',
          },
        ])
      },
    })
  }, [online, project.id])

  useEffect(() => {
    if (online) void api.listAgentProfiles().then(setProfiles).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  async function loadProvider() {
    try {
      setProvider(await api.getActiveProvider())
    } catch {
      /* offline */
    }
  }
  useEffect(() => {
    if (online) void loadProvider()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  async function loadThreads() {
    setThreads((await api.listThreads()) ?? [])
  }

  useEffect(() => {
    if (online) void loadThreads()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  // Deep-link: a cockpit click can request a specific thread — open it once threads arrive.
  const [linked, setLinked] = useState(false)
  useEffect(() => {
    if (linked || !initialThread) return
    const t = threads.find((th) => th.id === initialThread)
    if (t) {
      setLinked(true)
      void open(t)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialThread, threads, linked])

  async function refresh(t: Thread) {
    const d = await api.getThread(t.id)
    setMessages(d.messages ?? []) // Go serializes an empty slice as null
    setCurrent(d.thread)
    if (d.thread.status === 'awaiting_approval') {
      const aps = (await api.listApprovals()) ?? []
      setPending(aps.find((a) => a.thread_id === t.id) ?? null)
    } else {
      setPending(null)
    }
  }

  async function open(t: Thread) {
    setError(null)
    try {
      await refresh(t)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function newThread() {
    try {
      const label = profiles.find((p) => p.id === profileId)?.name ?? 'Analyst'
      const t = await api.createThread(project.id, label, profileId)
      await loadThreads()
      await open(t)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  // Retire a chat from the active list. Default is archive: it stays in the project's database (transcript
  // intact) for auditability, just hidden from this list. Shift-click purges permanently instead — a
  // deliberate, unrecoverable delete that also leaves the audit record. The control lives on the chip as a
  // span (not a nested button — the chip is itself a button); stopPropagation keeps it off the open handler.
  // Either way, drop back to the no-thread state if the open thread is the one going away.
  async function retireThread(t: Thread, e: ReactMouseEvent) {
    e.stopPropagation()
    const name = t.title || 'Analyst'
    const purge = e.shiftKey
    const ok = purge
      ? window.confirm(`Permanently delete thread "${name}"? Its messages can't be recovered and it leaves the project's audit record.\n\nTo archive instead (kept for audit), cancel and click without holding Shift.`)
      : window.confirm(`Archive thread "${name}"? It leaves this list but stays in the project record for auditability.`)
    if (!ok) return
    try {
      await (purge ? api.deleteThread(t.id) : api.archiveThread(t.id))
      if (current?.id === t.id) {
        setCurrent(null)
        setMessages([])
        setPending(null)
      }
      await loadThreads()
    } catch (err) {
      setError((err as Error).message)
    }
  }

  async function send(e: FormEvent) {
    e.preventDefault()
    if (!current || !input.trim()) return
    const text = input.trim()
    // Optimistically show the user's message and start streaming the reply's steps as they arrive.
    setMessages((ms) => [
      ...ms,
      { id: `stream:${streamKey.current++}`, thread_id: current.id, seq: ms.length, role: 'user', content: text, created_at: '' },
    ])
    setInput('')
    setBusy(true)
    streamingRef.current = true
    setStreamingText('')
    try {
      await api.sendMessage(current.id, text, getView?.())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      // Stop live appends before the authoritative refresh so a late frame can't duplicate a row.
      streamingRef.current = false
      try {
        await refresh(current)
        await loadThreads()
      } catch (e) {
        setError((e as Error).message)
      }
      setStreamingText('')
      setBusy(false)
    }
  }

  async function decide(decision: 'approve' | 'deny') {
    if (!pending || !current) return
    setBusy(true)
    streamingRef.current = true
    setStreamingText('')
    try {
      await api.decideApproval(pending.id, decision)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      streamingRef.current = false
      try {
        await refresh(current)
        await loadThreads()
      } catch (e) {
        setError((e as Error).message)
      }
      setStreamingText('')
      setBusy(false)
    }
  }

  // Docked on the right of the Workbench (ADR-0015): always present, stays
  // mounted across surface navigation so threads and streaming survive.
  if (collapsed) {
    return (
      <aside className="wb-analyst collapsed">
        <div className="wb-an-collapsed" onClick={() => setCollapsed(false)} title="Open Analyst">
          <span>◆</span>
          {pending && <span className="n">⏸</span>}
        </div>
      </aside>
    )
  }

  const modelLabel = provider ? provider.model || provider.type : '…'

  return (
    <aside className="wb-analyst" style={{ width: panelWidth, minWidth: panelWidth }}>
      <div className="wb-an-resize" onMouseDown={startResize} title="Drag to resize the Analyst" />
      <div className="wb-an-head">
        <span className="title">◆ Analyst</span>
        <span className="grow" />
        {onDriveChange && (
          <button
            className={`wb-an-drive ${drive ? 'on' : ''}`}
            onClick={() => onDriveChange(!drive)}
            aria-pressed={!!drive}
            title={
              drive
                ? 'Analyst can move your view to show you evidence — click to take back the wheel'
                : 'Let the Analyst navigate the workbench for you (it opens findings/code as it explains). Your clicks always win.'
            }
          >
            🕹 {drive ? 'Driving' : 'Drive'}
          </button>
        )}
        <select
          className={`wb-an-autonomy ${autonomy}`}
          value={autonomy}
          onChange={(e) => changeAutonomy(e.target.value)}
          disabled={!online}
          title="Autonomy — how much the Analyst does without asking. Cautious: it confirms outbound requests and running code/scanners. Trusted: it runs those freely too. Reversible actions always run; scope and data-egress limits always apply."
        >
          <option value="cautious">🛡 Cautious</option>
          <option value="trusted">⚡ Trusted</option>
        </select>
        {profiles.length > 0 && (
          <select
            className="wb-an-profile"
            value={profileId}
            onChange={(e) => setProfileId(e.target.value)}
            disabled={!online}
            title="Agent profile for a new thread"
          >
            {profiles.map((p) => (
              <option key={p.id} value={p.id} title={p.description}>{p.name}</option>
            ))}
          </select>
        )}
        <button onClick={newThread} disabled={!online} title={`New ${profiles.find((p) => p.id === profileId)?.name ?? 'Analyst'} thread`}>＋ Thread</button>
        <button onClick={() => setCollapsed(true)} title="Collapse">⟩</button>
      </div>

      <div className={`wb-an-model ${provider && !provider.configured ? 'unconfigured' : ''}`} title="Model for this chat — set by the default tag in Settings → Models & Providers">
        <span className={`dot ${provider?.is_local ? 'local' : ''}`} />
        <span className="m">{modelLabel}</span>
        {provider && !provider.configured && <span className="warn">⚠ not configured</span>}
      </div>

      <div className="wb-an-threads">
        {threads.map((t) => (
          <button key={t.id} className={`wb-an-chip ${current?.id === t.id ? 'on' : ''}`} onClick={() => open(t)}>
            <span className={`tstatus tstatus-${t.status}`} />
            <span className="tname">{t.title || 'Analyst'}</span>
            <span className="tarch" title="Archive (kept in the project record) — Shift-click to delete permanently" onClick={(e) => retireThread(t, e)}>🗄</span>
          </button>
        ))}
        {threads.length === 0 && (
          <button className="wb-an-chip new" onClick={newThread} disabled={!online}>＋ New thread</button>
        )}
      </div>

      {error && <div className="banner error">⚠ {error}</div>}
      {!current ? (
        <div className="empty">Start a thread to chat with the Analyst.</div>
      ) : (
        <>
          <div className="messages">
            {(messages ?? []).filter((m) => m.role !== 'system').map((m) => (
              <Message key={m.id} m={m} />
            ))}
            {streamingText && (
              <div className="msg analyst streaming">
                <b>Analyst</b>
                <div>
                  <Markdown source={streamingText} />
                  <span className="stream-caret">▍</span>
                </div>
              </div>
            )}
          </div>

          {pending && <ApprovalCard approval={pending} busy={busy} onDecide={decide} />}

          <form className="composer" onSubmit={send}>
            <input
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder={pending ? 'Resolve the approval to continue…' : 'Ask the Analyst…'}
              disabled={!online || busy || !!pending}
            />
            <button type="submit" disabled={!online || busy || !!pending || !input.trim()}>
              {busy ? '…' : 'Send'}
            </button>
          </form>
        </>
      )}
    </aside>
  )
}

// showLabel renders a `show` (navigation) tool call as a human phrase — "opened finding <id>", "opened
// <path:line>", "opened <surface>" — for the live walkthrough trail.
function showLabel(args: Record<string, unknown>): string {
  const kind = String(args?.kind ?? '')
  const id = String(args?.id ?? '')
  const loc = String(args?.location ?? '')
  if (kind === 'code') return `opened ${loc || id}`
  if (kind === 'surface') return `opened the ${id} view`
  return `opened ${kind}${id ? ` ${id}` : ''}`
}

function Message({ m }: { m: Msg }) {
  // A tool-result turn (canonical, ADR-0017): its content is the tool's output or an error.
  if (m.role === 'tool') {
    const label = m.content.startsWith('Tool ') ? m.content.split('\n')[0] : 'tool result'
    return <div className={'msg tool' + (m.tool_error ? ' error' : '')}>🔧 {label}</div>
  }
  if (m.role === 'assistant') {
    // An assistant turn that requested a tool. It may also carry prose — the agent narrating what it's about
    // to do ("let me open the finding") — which we show so the walkthrough reads as an explanation, not just
    // a mechanical tool trace. A `show` call is navigation, so it reads as "opened X", not "wants to run".
    if (m.tool_calls && m.tool_calls.length > 0) {
      const c = m.tool_calls[0]
      return (
        <div className="msg propose">
          {m.content.trim() && <div className="propose-note"><Markdown source={m.content} /></div>}
          <div className="propose-tool">{c.tool === 'show' ? `📂 ${showLabel(c.args)}` : <>⚙ wants to run <b>{c.tool}</b></>}</div>
        </div>
      )
    }
    // An assistant turn with neither prose nor a tool call — the model returned an empty completion. Show a
    // clear placeholder rather than a silent blank bubble that reads as a hang.
    if (!m.content.trim()) {
      return (
        <div className="msg analyst empty">
          <b>Analyst</b>
          <div className="muted">(no response — ask again or rephrase)</div>
        </div>
      )
    }
    return (
      <div className="msg analyst">
        <b>Analyst</b>
        <div><Markdown source={m.content} /></div>
      </div>
    )
  }
  return (
    <div className="msg user">
      <b>You</b>
      <div>{m.content}</div>
    </div>
  )
}
