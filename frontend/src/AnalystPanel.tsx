import { useEffect, useState, type FormEvent } from 'react'
import { api, ActiveProvider, Approval, Msg, Project, Thread } from './api'
import { ProviderSettings } from './ProviderSettings'

export function AnalystPanel({ project, online }: { project: Project; online: boolean }) {
  const [threads, setThreads] = useState<Thread[]>([])
  const [current, setCurrent] = useState<Thread | null>(null)
  const [messages, setMessages] = useState<Msg[]>([])
  const [pending, setPending] = useState<Approval | null>(null)
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [collapsed, setCollapsed] = useState(false)
  const [provider, setProvider] = useState<ActiveProvider | null>(null)
  const [showProviders, setShowProviders] = useState(false)
  const [error, setError] = useState<string | null>(null)

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
      const t = await api.createThread(project.id, 'Analyst')
      await loadThreads()
      await open(t)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function send(e: FormEvent) {
    e.preventDefault()
    if (!current || !input.trim()) return
    setBusy(true)
    try {
      await api.sendMessage(current.id, input.trim())
      setInput('')
      await refresh(current)
      await loadThreads()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function decide(decision: 'approve' | 'deny') {
    if (!pending || !current) return
    setBusy(true)
    try {
      await api.decideApproval(pending.id, decision)
      await refresh(current)
      await loadThreads()
    } catch (e) {
      setError((e as Error).message)
    } finally {
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
    <aside className="wb-analyst">
      <div className="wb-an-head">
        <span className="title">◆ Analyst</span>
        <span className="grow" />
        <button className={showProviders ? 'on' : ''} onClick={() => setShowProviders((v) => !v)} title="Model / provider">⚙</button>
        <button onClick={newThread} disabled={!online} title="New thread">＋ Thread</button>
        <button onClick={() => setCollapsed(true)} title="Collapse">⟩</button>
      </div>

      <button className={`wb-an-model ${provider && !provider.configured ? 'unconfigured' : ''}`} onClick={() => setShowProviders(true)} title="Change model / provider">
        <span className={`dot ${provider?.is_local ? 'local' : ''}`} />
        <span className="m">{modelLabel}</span>
        {provider && !provider.configured && <span className="warn">⚠ not configured</span>}
      </button>

      {showProviders ? (
        <ProviderSettings
          online={online}
          projectId={project.id}
          onClose={() => setShowProviders(false)}
          onChanged={loadProvider}
        />
      ) : (
      <>
      <div className="wb-an-threads">
        {threads.map((t) => (
          <button key={t.id} className={`wb-an-chip ${current?.id === t.id ? 'on' : ''}`} onClick={() => open(t)}>
            <span className={`tstatus tstatus-${t.status}`} />
            <span className="tname">{t.title || 'Analyst'}</span>
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
          </div>

          {pending && (
            <div className="approval-card">
              <div className="ac-title">⏸ Approval required</div>
              <code>
                {pending.tool} {JSON.stringify(pending.args)}
              </code>
              <div className="ac-btns">
                <button className="ok" disabled={busy} onClick={() => decide('approve')}>
                  ✓ Approve
                </button>
                <button className="no" disabled={busy} onClick={() => decide('deny')}>
                  ✕ Deny
                </button>
              </div>
            </div>
          )}

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
      </>
      )}
    </aside>
  )
}

function Message({ m }: { m: Msg }) {
  // A tool-result turn (canonical, ADR-0017): its content is the tool's output or an error.
  if (m.role === 'tool') {
    const label = m.content.startsWith('Tool ') ? m.content.split('\n')[0] : 'tool result'
    return <div className={'msg tool' + (m.tool_error ? ' error' : '')}>🔧 {label}</div>
  }
  if (m.role === 'assistant') {
    // An assistant turn that requested a tool carries structured tool_calls (no prose).
    if (m.tool_calls && m.tool_calls.length > 0) {
      const c = m.tool_calls[0]
      return (
        <div className="msg propose">
          ⚙ wants to run <b>{c.tool}</b>
        </div>
      )
    }
    return (
      <div className="msg analyst">
        <b>Analyst</b>
        <div>{m.content}</div>
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
