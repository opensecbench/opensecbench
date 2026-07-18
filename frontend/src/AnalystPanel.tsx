import { useEffect, useState, type FormEvent } from 'react'
import { api, Approval, Msg, Project, Thread } from './api'

export function AnalystPanel({ project, online }: { project: Project; online: boolean }) {
  const [threads, setThreads] = useState<Thread[]>([])
  const [current, setCurrent] = useState<Thread | null>(null)
  const [messages, setMessages] = useState<Msg[]>([])
  const [pending, setPending] = useState<Approval | null>(null)
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function loadThreads() {
    setThreads((await api.listThreads()) ?? [])
  }

  useEffect(() => {
    if (online) void loadThreads()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  async function refresh(t: Thread) {
    const d = await api.getThread(t.id)
    setMessages(d.messages)
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

  return (
    <div className="analyst">
      <aside className="threads">
        <button className="new-thread" onClick={newThread} disabled={!online}>
          ＋ New thread
        </button>
        {threads.map((t) => (
          <button key={t.id} className={`thread-item ${current?.id === t.id ? 'on' : ''}`} onClick={() => open(t)}>
            <span className={`tstatus tstatus-${t.status}`} />
            <span className="tname">{t.title || 'Analyst'}</span>
          </button>
        ))}
      </aside>

      <div className="chat">
        {error && <div className="banner error">⚠ {error}</div>}
        {!current ? (
          <div className="empty">Start a thread to chat with the Analyst.</div>
        ) : (
          <>
            <div className="messages">
              {messages.filter((m) => m.role !== 'system').map((m) => (
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
      </div>
    </div>
  )
}

function Message({ m }: { m: Msg }) {
  if (m.role === 'user' && m.content.startsWith('Tool ')) {
    return <div className="msg tool">🔧 {m.content.split('\n')[0]}</div>
  }
  if (m.role === 'assistant') {
    const parsed = tryParse(m.content)
    if (parsed?.tool) {
      return (
        <div className="msg propose">
          ⚙ wants to run <b>{parsed.tool}</b>
        </div>
      )
    }
    return (
      <div className="msg analyst">
        <b>Analyst</b>
        <div>{parsed?.answer ?? m.content}</div>
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

function tryParse(s: string): { tool?: string; answer?: string } | null {
  const i = s.indexOf('{')
  const j = s.lastIndexOf('}')
  if (i < 0 || j <= i) return null
  try {
    return JSON.parse(s.slice(i, j + 1))
  } catch {
    return null
  }
}
