import { useEffect, useMemo, useRef, useState } from 'react'
import { api, HTTPExchange, ProxyStatus, Project } from './api'
import { actionsFor, type ActionContext } from './exchangeActions'
import { hasNativeBrowserLaunch, openProxyBrowser } from './native'

const METHODS = ['', 'GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

function statusClass(status?: number): string {
  if (status == null) return ''
  if (status >= 400) return 'failed'
  if (status >= 200 && status < 300) return 'succeeded'
  return 'active'
}

// The Proxy: capture + a first-class, filterable history with a detail view. Actions on an
// exchange (Send to Replay, Save as evidence, Copy as curl) come from the shared action registry
// (ADR-0016), so this surface never hard-codes what you can do with a request.
export function ProxyTab({
  project,
  online,
  onError,
  onSendToReplay,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
  onSendToReplay: (ex: HTTPExchange) => void
}) {
  const [status, setStatus] = useState<ProxyStatus>({ running: false })
  const [captured, setCaptured] = useState<HTTPExchange[]>([])
  const [busy, setBusy] = useState(false)
  const [method, setMethod] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [q, setQ] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [toast, setToast] = useState<string | null>(null)

  async function loadList() {
    try {
      const list =
        (await api.listExchanges(project.id, {
          origin: 'proxy',
          method: method || undefined,
          status: statusFilter ? Number(statusFilter) : undefined,
          q: q || undefined,
          limit: 300,
        })) ?? []
      setCaptured(list)
    } catch (e) {
      onError((e as Error).message)
    }
  }

  // A live event may arrive while a filter is active; decide client-side whether it belongs in the
  // current view (mirrors the server filter). A ref keeps the stream handler reading fresh filters
  // without re-subscribing on every keystroke.
  const filterRef = useRef({ method, statusFilter, q })
  filterRef.current = { method, statusFilter, q }
  function matchesFilter(ex: HTTPExchange): boolean {
    const f = filterRef.current
    return (
      ex.origin === 'proxy' &&
      (!f.method || ex.method === f.method) &&
      (!f.statusFilter || ex.status === Number(f.statusFilter)) &&
      (!f.q || ex.url.toLowerCase().includes(f.q.toLowerCase()))
    )
  }

  // Initial proxy status (then kept current by 'proxy' events + toggle, no polling).
  useEffect(() => {
    if (!online) return
    api.getProxy(project.id).then(setStatus).catch((e) => onError((e as Error).message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  // Fetch the filtered history on mount and whenever a filter changes; the live stream keeps it fresh.
  useEffect(() => {
    if (!online) return
    void loadList()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id, method, statusFilter, q])

  // Live push (SSE): upsert captured exchanges as they happen; reflect proxy start/stop. Subscribe
  // once per project — filters are applied via the ref, so typing never tears down the stream.
  useEffect(() => {
    if (!online) return
    const close = api.subscribeProjectEvents(project.id, {
      exchange: (ex) => {
        if (!matchesFilter(ex)) return
        setCaptured((prev) => [ex, ...prev.filter((e) => e.id !== ex.id)].slice(0, 300))
      },
      proxy: setStatus,
    })
    return close
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  async function toggle() {
    setBusy(true)
    try {
      setStatus(status.running ? await api.stopProxy(project.id) : await api.startProxy(project.id))
      await loadList()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const selected = useMemo(() => captured.find((e) => e.id === selectedId) ?? null, [captured, selectedId])

  const ctx: ActionContext = {
    openReplay: (ex) => onSendToReplay(ex),
    saveEvidence: async (ex) => {
      try {
        await api.saveExchangeEvidence(ex.id, '')
      } catch (e) {
        onError((e as Error).message)
      }
    },
    copy: (text) => {
      void navigator.clipboard?.writeText(text)
    },
    notify: (m) => {
      setToast(m)
      setTimeout(() => setToast(null), 1800)
    },
  }

  return (
    <div className="proxy">
      <div className="proxy-toolbar">
        <button className={status.running ? 'stop' : ''} onClick={toggle} disabled={!online || busy}>
          {busy ? '…' : status.running ? '■ Stop proxy' : '▶ Start proxy'}
        </button>
        {status.running && (
          <span className="mono muted">
            listening on <b>127.0.0.1:{status.port}</b>
          </span>
        )}
        {status.running && hasNativeBrowserLaunch() && status.ca_spki_sha256 && (
          <button
            className="ghost-btn"
            onClick={() => {
              void openProxyBrowser(status.port ?? 0, status.ca_spki_sha256 ?? '').catch((e) => onError((e as Error).message))
            }}
          >
            Open browser
          </button>
        )}
        <a className="link" href={api.proxyCAURL()} target="_blank" rel="noreferrer">download CA cert</a>
        <span className="spacer" />
        <span className="muted">{captured.length} request{captured.length === 1 ? '' : 's'}</span>
      </div>

      <div className="proxy-filters">
        <select value={method} onChange={(e) => setMethod(e.target.value)}>
          {METHODS.map((m) => (
            <option key={m} value={m}>{m || 'any method'}</option>
          ))}
        </select>
        <input className="mono" placeholder="status (e.g. 200)" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value.replace(/[^0-9]/g, ''))} />
        <input className="mono grow" placeholder="filter URL…" value={q} onChange={(e) => setQ(e.target.value)} />
        {(method || statusFilter || q) && (
          <button className="ghost-btn" onClick={() => { setMethod(''); setStatusFilter(''); setQ('') }}>clear</button>
        )}
      </div>

      <div className="proxy-split">
        <div className="proxy-list">
          {captured.length === 0 ? (
            <div className="empty">{status.running ? 'Waiting for traffic…' : 'No captured traffic yet. Start the proxy and route a browser through it.'}</div>
          ) : (
            <table className="proxy-table">
              <thead>
                <tr><th>status</th><th>method</th><th>URL</th><th>ms</th></tr>
              </thead>
              <tbody>
                {captured.map((e) => (
                  <tr key={e.id} className={selectedId === e.id ? 'sel' : ''} onClick={() => setSelectedId(e.id)}>
                    <td><span className={`badge ${statusClass(e.status)}`}>{e.status ?? '—'}</span></td>
                    <td className="kind">{e.method}</td>
                    <td className="mono url">{e.url}</td>
                    <td className="muted">{e.duration_ms ?? ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <div className="proxy-detail">
          {!selected ? (
            <div className="empty">Select a request to inspect it.</div>
          ) : (
            <>
              <div className="proxy-actions">
                {actionsFor(selected).map((a) => (
                  <button key={a.id} className="ghost-btn" title={a.label} onClick={() => void a.run(selected, ctx)}>
                    {a.icon} {a.label}
                  </button>
                ))}
              </div>
              <div className="proxy-pane">
                <div className="proxy-lbl">Request</div>
                <pre className="mono">{selected.method} {selected.url}
{selected.request_headers}
{selected.request_body ? '\n' + selected.request_body : ''}</pre>
              </div>
              <div className="proxy-pane">
                <div className="proxy-lbl">
                  Response {selected.status != null && <span className={`badge ${statusClass(selected.status)}`}>{selected.status}</span>}
                  {selected.duration_ms != null && <span className="muted"> · {selected.duration_ms} ms</span>}
                </div>
                <pre className="mono">{selected.response_headers}
{selected.response_body ? '\n' + selected.response_body : ''}</pre>
              </div>
            </>
          )}
        </div>
      </div>

      {toast && <div className="proxy-toast">{toast}</div>}
    </div>
  )
}
