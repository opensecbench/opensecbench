import { useEffect, useMemo, useRef, useState } from 'react'
import { api, CertSummary, HTTPExchange, ProxyStatus, Project, RunnerView } from './api'
import { actionsFor, type ActionContext } from './exchangeActions'
import { ContextMenu, useContextMenu } from './ContextMenu'
import { hasNativeBrowserLaunch, openProxyBrowser, downloadArtifact } from './native'
import { TrafficRules } from './TrafficRules'

const METHODS = ['', 'GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

// tlsBadge summarizes a captured upstream cert (review #6): a green lock when valid, a red warning listing
// the problems (expired / self-signed / untrusted CA / hostname mismatch) otherwise. Null for plain HTTP.
function tlsBadge(tls?: string): { icon: string; cls: string; title: string } | null {
  if (!tls) return null
  let c: CertSummary
  try {
    c = JSON.parse(tls)
  } catch {
    return null
  }
  if (c.valid) return { icon: '🔒', cls: 'tls-ok', title: `TLS OK — ${c.subject} (issuer ${c.issuer}) · expires ${c.not_after}` }
  const issues: string[] = []
  if (c.expired) issues.push('expired')
  if (c.self_signed) issues.push('self-signed')
  else if (c.untrusted) issues.push('untrusted CA')
  if (c.hostname_mismatch) issues.push('hostname mismatch')
  return { icon: '⚠', cls: 'tls-bad', title: `TLS: ${issues.join(', ') || 'invalid'} — ${c.subject} (issuer ${c.issuer})` }
}

function statusClass(status?: number): string {
  if (status == null) return ''
  if (status >= 400) return 'failed'
  if (status >= 200 && status < 300) return 'succeeded'
  return 'active'
}

// The Proxy: capture + a first-class, filterable history with a detail view. Actions on an
// exchange (Send to Replay, Save as evidence, Copy as curl) come from the shared action registry
// (ADR-0016), so this surface never hard-codes what you can do with a request.
const MAX_BODY_CHARS = 200_000
// clipBody caps the displayed body so a multi-MB response doesn't render a giant DOM text node — that
// (plus recomputing the selection on every traffic flush) is what made clicking a request slow. The full
// body is still captured; this only bounds what's shown.
function clipBody(s: string): string {
  return s.length > MAX_BODY_CHARS ? s.slice(0, MAX_BODY_CHARS) + `\n\n…(${s.length - MAX_BODY_CHARS} more bytes — truncated for display)` : s
}

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
  const [runners, setRunners] = useState<RunnerView[]>([])
  const [via, setVia] = useState('') // '' = local host; else an egress runner id (ADR-0026)
  const [method, setMethod] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [q, setQ] = useState('')
  const [selected, setSelected] = useState<HTTPExchange | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const rowMenu = useContextMenu<HTTPExchange>()
  const [showRules, setShowRules] = useState(false)

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
  //
  // A busy target can emit hundreds of exchanges/second. Rendering the (up to 300-row) list on every
  // event pegged a CPU core, so events are coalesced into a buffer and flushed at most ~5×/s — the render
  // rate is decoupled from the event rate.
  const pendingRef = useRef<HTTPExchange[]>([])
  const flushTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => {
    if (!online) return
    const flush = () => {
      flushTimer.current = null
      const batch = pendingRef.current
      if (batch.length === 0) return
      pendingRef.current = []
      setCaptured((prev) => {
        const seen = new Set<string>()
        const merged: HTTPExchange[] = []
        // Newest event first: the buffer is in arrival (oldest→newest) order, so walk it reversed.
        for (let i = batch.length - 1; i >= 0; i--) {
          if (!seen.has(batch[i].id)) { seen.add(batch[i].id); merged.push(batch[i]) }
        }
        for (const ex of prev) {
          if (!seen.has(ex.id)) { seen.add(ex.id); merged.push(ex) }
        }
        return merged.slice(0, 300)
      })
    }
    const close = api.subscribeProjectEvents(project.id, {
      exchange: (ex) => {
        if (!matchesFilter(ex)) return
        pendingRef.current.push(ex)
        // Only the newest ~300 can survive the merge; cap the buffer so an extreme burst can't grow it.
        if (pendingRef.current.length > 400) pendingRef.current.splice(0, pendingRef.current.length - 400)
        if (flushTimer.current === null) flushTimer.current = setTimeout(flush, 200)
      },
      proxy: setStatus,
    })
    return () => {
      close()
      if (flushTimer.current !== null) { clearTimeout(flushTimer.current); flushTimer.current = null }
      pendingRef.current = []
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  // Enrolled runners offer alternate egress vantages for a proxy session (ADR-0026).
  useEffect(() => {
    if (online) api.listRunners().then((r) => setRunners(r ?? [])).catch(() => {})
  }, [online])

  async function toggle() {
    setBusy(true)
    try {
      setStatus(status.running ? await api.stopProxy(project.id) : await api.startProxy(project.id, 0, via || undefined))
      await loadList()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // Sort state for the capture table. Default: newest received first (the capture list already arrives that
  // way, so 'received' desc is a no-op pass-through).
  const [sortKey, setSortKey] = useState<'received' | 'status' | 'method' | 'url' | 'ms'>('received')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc')
  function toggleSort(key: typeof sortKey) {
    if (key === sortKey) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    else { setSortKey(key); setSortDir(key === 'url' || key === 'method' ? 'asc' : 'desc') }
  }
  // `captured` is newest-first as received; index encodes arrival order for the 'received' sort.
  const sorted = useMemo(() => {
    const rows = captured.map((e, i) => ({ e, ord: captured.length - i })) // ord: higher = more recent
    const dir = sortDir === 'asc' ? 1 : -1
    rows.sort((a, b) => {
      let c = 0
      switch (sortKey) {
        case 'received': c = a.ord - b.ord; break
        case 'status': c = (a.e.status ?? 0) - (b.e.status ?? 0); break
        case 'method': c = a.e.method.localeCompare(b.e.method); break
        case 'url': c = a.e.url.localeCompare(b.e.url); break
        case 'ms': c = (a.e.duration_ms ?? 0) - (b.e.duration_ms ?? 0); break
      }
      return c !== 0 ? c * dir : (a.ord - b.ord) * -1 // stable tiebreak: most recent first
    })
    return rows
  }, [captured, sortKey, sortDir])

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
        {!status.running && runners.some((r) => r.online) && (
          <select value={via} onChange={(e) => setVia(e.target.value)} title="Egress vantage" disabled={!online || busy}>
            <option value="">egress: local host</option>
            {runners.filter((r) => r.online).map((r) => (
              <option key={r.id} value={r.id}>egress: {r.name}</option>
            ))}
          </select>
        )}
        <button className={status.running ? 'stop' : ''} onClick={toggle} disabled={!online || busy}>
          {busy ? '…' : status.running ? '■ Stop proxy' : '▶ Start proxy'}
        </button>
        {status.running && (
          <span className="mono muted">
            listening on <b>127.0.0.1:{status.port}</b>
            {status.egress && <> · via <b>{runners.find((r) => r.id === status.egress)?.name ?? status.egress}</b></>}
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
        <button
          className="link"
          onClick={() =>
            void downloadArtifact('/v1/proxy/ca', 'opensecbench-ca.crt').catch((e) => onError((e as Error).message))
          }
        >
          download CA cert
        </button>
        <button className={`ghost-btn ${showRules ? 'on' : ''}`} onClick={() => setShowRules((v) => !v)}>
          🚦 Traffic rules
        </button>
        <span className="spacer" />
        <span className="muted">{captured.length} request{captured.length === 1 ? '' : 's'}</span>
      </div>

      {showRules && <TrafficRules project={project} online={online} onError={onError} />}

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
                <tr className="sortable">
                  {([
                    ['received', '#'],
                    ['status', 'status'],
                    ['method', 'method'],
                    ['url', 'URL'],
                    ['ms', 'ms'],
                  ] as const).map(([key, label]) => (
                    <th key={key} onClick={() => toggleSort(key)} className={sortKey === key ? 'on' : ''}>
                      {label}{sortKey === key && <span className="sort-arrow">{sortDir === 'asc' ? ' ▲' : ' ▼'}</span>}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {sorted.map(({ e, ord }) => (
                  <tr
                    key={e.id}
                    className={`${selected?.id === e.id ? 'sel' : ''} ${e.in_scope === false ? 'oos' : ''}`}
                    onClick={() => setSelected(e)}
                    onContextMenu={(ev) => { setSelected(e); rowMenu.open(ev, e) }}
                  >
                    <td className="muted num">{ord}</td>
                    <td><span className={`badge ${statusClass(e.status)}`}>{e.status ?? '—'}</span></td>
                    <td className="kind">{e.method}</td>
                    <td className="mono url">{e.in_scope === false && <span className="oos-tag" title="out of scope">out</span>}{(() => { const b = tlsBadge(e.tls); return b ? <span className={`tls-badge ${b.cls}`} title={b.title}>{b.icon}</span> : null })()}{e.url}</td>
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
{selected.request_body ? '\n' + clipBody(selected.request_body) : ''}</pre>
              </div>
              <div className="proxy-pane">
                <div className="proxy-lbl">
                  Response {selected.status != null && <span className={`badge ${statusClass(selected.status)}`}>{selected.status}</span>}
                  {selected.duration_ms != null && <span className="muted"> · {selected.duration_ms} ms</span>}
                </div>
                <pre className="mono">{selected.response_headers}
{selected.response_body ? '\n' + clipBody(selected.response_body) : ''}</pre>
              </div>
            </>
          )}
        </div>
      </div>

      {rowMenu.menu && (
        <ContextMenu
          x={rowMenu.menu.x}
          y={rowMenu.menu.y}
          onClose={rowMenu.close}
          items={actionsFor(rowMenu.menu.payload).map((a) => ({
            id: a.id,
            label: a.label,
            icon: a.icon,
            onSelect: () => a.run(rowMenu.menu!.payload, ctx),
          }))}
        />
      )}

      {toast && <div className="proxy-toast">{toast}</div>}
    </div>
  )
}
