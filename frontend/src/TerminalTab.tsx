import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api, Project, Session, wsURL } from './api'

export function TerminalTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [sessions, setSessions] = useState<Session[]>([])
  const [active, setActive] = useState<Session | null>(null)
  const [busy, setBusy] = useState(false)

  const termHost = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const wsRef = useRef<WebSocket | null>(null)

  async function reload() {
    setSessions((await api.listSessions(project.id)) ?? [])
  }

  useEffect(() => {
    if (online) void reload().catch((e) => onError((e as Error).message))
    return () => teardown()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  function teardown() {
    wsRef.current?.close()
    wsRef.current = null
    termRef.current?.dispose()
    termRef.current = null
    fitRef.current = null
  }

  async function open() {
    setBusy(true)
    try {
      const s = await api.openSession(project.id, 'human')
      await reload()
      attach(s)
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  function attach(s: Session) {
    teardown()
    setActive(s)

    const term = new Terminal({
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 13,
      theme: { background: '#0b0e14', foreground: '#c7d0de', cursor: '#4aa8ff' },
      cursorBlink: true,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    termRef.current = term
    fitRef.current = fit
    // Mount after the host div renders.
    requestAnimationFrame(() => {
      if (!termHost.current) return
      term.open(termHost.current)
      fit.fit()
      openSocket(s, term, fit)
    })
  }

  function openSocket(s: Session, term: Terminal, fit: FitAddon) {
    const ws = new WebSocket(wsURL(`/v1/sessions/${s.id}/ws`))
    ws.binaryType = 'arraybuffer'
    wsRef.current = ws
    const enc = new TextEncoder()
    const dec = new TextDecoder()

    ws.onopen = () => {
      sendResize(ws, term)
      term.focus()
    }
    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) term.write(dec.decode(ev.data))
      else term.write(String(ev.data))
    }
    ws.onclose = () => {
      term.write('\r\n\x1b[90m[session closed]\x1b[0m\r\n')
      void reload()
    }
    term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d))
    })
    term.onResize(() => sendResize(ws, term))
    const onWinResize = () => {
      try {
        fit.fit()
      } catch {
        /* host not laid out yet */
      }
    }
    window.addEventListener('resize', onWinResize)
    ws.addEventListener('close', () => window.removeEventListener('resize', onWinResize))
  }

  function sendResize(ws: WebSocket, term: Terminal) {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'resize', rows: term.rows, cols: term.cols }))
    }
  }

  async function close() {
    if (!active) return
    try {
      wsRef.current?.close()
      await api.closeSession(active.id)
      await reload()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  async function saveEvidence() {
    if (!active) return
    try {
      await api.saveSessionEvidence(active.id, '')
    } catch (e) {
      onError((e as Error).message)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">Terminal</div>
      <p className="hint">
        A shell inside a sandboxed container (no network by default). The full transcript is captured
        on close for audit and can be saved as evidence.
      </p>
      <div className="term-toolbar">
        <button onClick={open} disabled={!online || busy}>
          {busy ? 'Opening…' : '＋ New terminal'}
        </button>
        {active && (
          <>
            <button className="ghost-btn" onClick={close}>Close</button>
            <button className="link" onClick={saveEvidence}>save transcript as evidence</button>
            <span className="muted mono">{active.container}</span>
          </>
        )}
      </div>
      <div ref={termHost} className={`term-host ${active ? 'on' : ''}`} />

      {sessions.length > 0 && (
        <ul className="rows term-history">
          {sessions.map((s) => (
            <li key={s.id} className="row-item">
              <span className={`badge ${s.status === 'active' ? 'active' : 'succeeded'}`}>{s.status}</span>
              <span className="row-title mono">{s.container}</span>
              {s.status === 'closed' && s.transcript_artifact_id && (
                <a className="link" href={api.artifactContentURL(s.transcript_artifact_id)} target="_blank" rel="noreferrer">
                  transcript
                </a>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
