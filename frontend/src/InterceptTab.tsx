import { useEffect, useMemo, useState } from 'react'
import { api, HeldItem, InterceptState, Project } from './api'

// The Intercept surface (ADR-0016 Step 3): arm request/response interception, work the queue of held
// traffic, and edit → Forward or Drop each item. Live over the SSE hub — no polling.
export function InterceptTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [state, setState] = useState<InterceptState>({ requests: false, responses: false, held: [] })
  const [running, setRunning] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [draft, setDraft] = useState<HeldItem | null>(null)

  useEffect(() => {
    if (!online) return
    api.getProxy(project.id).then((s) => setRunning(s.running)).catch(() => {})
    api.getIntercept(project.id).then(setState).catch((e) => onError((e as Error).message))
    const close = api.subscribeProjectEvents(project.id, {
      proxy: (s) => setRunning(s.running),
      interceptState: setState,
      held: (h) => setState((s) => ({ ...s, held: [...s.held.filter((x) => x.id !== h.id), h] })),
      resolved: (id) => setState((s) => ({ ...s, held: s.held.filter((x) => x.id !== id) })),
    })
    return close
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, project.id])

  // Keep a selection; when the selected item resolves (leaves the queue), fall back to the first held.
  const selected = useMemo(() => state.held.find((h) => h.id === selectedId) ?? null, [state.held, selectedId])
  useEffect(() => {
    if (!selected && state.held.length) setSelectedId(state.held[0].id)
    if (state.held.length === 0) setSelectedId(null)
  }, [state.held, selected])

  // Load the selected held item into an editable draft (once per id).
  useEffect(() => {
    setDraft(selected ? { ...selected } : null)
  }, [selected?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  async function arm(requests: boolean, responses: boolean) {
    try {
      setState(await api.setIntercept(project.id, requests, responses))
    } catch (e) {
      onError((e as Error).message)
    }
  }

  async function resolve(action: 'forward' | 'drop') {
    if (!selected) return
    const d = draft ?? selected
    try {
      await api.resolveIntercept(project.id, selected.id, {
        action,
        method: d.method,
        url: d.url,
        request_headers: d.request_headers,
        request_body: d.request_body,
        status: d.status,
        response_headers: d.response_headers,
        response_body: d.response_body,
      })
      // the resolved event removes it from the queue
    } catch (e) {
      onError((e as Error).message)
    }
  }

  const set = (patch: Partial<HeldItem>) => setDraft((d) => (d ? { ...d, ...patch } : d))

  return (
    <div className="icept">
      <div className="icept-arm">
        <label className={`icept-toggle ${state.requests ? 'on' : ''}`}>
          <input type="checkbox" checked={state.requests} disabled={!online || !running} onChange={(e) => arm(e.target.checked, state.responses)} />
          Intercept requests
        </label>
        <label className={`icept-toggle ${state.responses ? 'on' : ''}`}>
          <input type="checkbox" checked={state.responses} disabled={!online || !running} onChange={(e) => arm(state.requests, e.target.checked)} />
          Intercept responses
        </label>
        <span className="spacer" />
        {!running ? (
          <span className="warnpill">⚠ start the proxy to intercept</span>
        ) : (
          <span className="muted">{state.held.length} held</span>
        )}
      </div>

      <div className="icept-split">
        <div className="icept-queue">
          {state.held.length === 0 ? (
            <div className="empty">
              {running && (state.requests || state.responses)
                ? 'Armed — waiting to hold traffic…'
                : 'Nothing held. Arm interception above, then route traffic through the proxy.'}
            </div>
          ) : (
            state.held.map((h) => (
              <button key={h.id} className={`icept-row ${selectedId === h.id ? 'sel' : ''}`} onClick={() => setSelectedId(h.id)}>
                <span className={`badge phase-${h.phase}`}>{h.phase === 'request' ? '→ req' : '← resp'}</span>
                <span className="kind">{h.method}</span>
                <span className="mono url">{h.url}</span>
              </button>
            ))
          )}
        </div>

        <div className="icept-detail">
          {!draft ? (
            <div className="empty">Select a held item to edit and forward or drop it.</div>
          ) : (
            <>
              <div className="icept-actions">
                <button className="btn-forward" onClick={() => resolve('forward')}>▷ Forward</button>
                <button className="btn-drop" onClick={() => resolve('drop')}>✕ Drop</button>
                <span className="muted">
                  {draft.phase === 'request' ? 'editing request →' : 'editing response ←'} {draft.method} {draft.url}
                </span>
              </div>

              {draft.phase === 'request' ? (
                <>
                  <div className="icept-line">
                    <input className="mono" style={{ width: 90 }} value={draft.method} onChange={(e) => set({ method: e.target.value })} />
                    <input className="mono grow" value={draft.url} onChange={(e) => set({ url: e.target.value })} />
                  </div>
                  <label className="icept-lbl">Request headers</label>
                  <textarea className="mono" rows={7} value={draft.request_headers} onChange={(e) => set({ request_headers: e.target.value })} />
                  <label className="icept-lbl">Request body</label>
                  <textarea className="mono" rows={8} value={draft.request_body} onChange={(e) => set({ request_body: e.target.value })} />
                </>
              ) : (
                <>
                  <div className="icept-line">
                    <label className="icept-lbl" style={{ padding: 0 }}>Status</label>
                    <input className="mono" style={{ width: 90 }} value={draft.status ?? 0} onChange={(e) => set({ status: Number(e.target.value.replace(/[^0-9]/g, '')) || 0 })} />
                  </div>
                  <label className="icept-lbl">Response headers</label>
                  <textarea className="mono" rows={7} value={draft.response_headers ?? ''} onChange={(e) => set({ response_headers: e.target.value })} />
                  <label className="icept-lbl">Response body</label>
                  <textarea className="mono" rows={8} value={draft.response_body ?? ''} onChange={(e) => set({ response_body: e.target.value })} />
                </>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
