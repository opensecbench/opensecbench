import { Markdown } from './Markdown'
import { Msg } from './api'

// MessageTurn renders one canonical turn (ADR-0017) — user / assistant / tool-result — for both the live
// Analyst chat (`chat`) and the after-the-fact Activity transcript (`transcript`). Sharing one renderer keeps
// the two from drifting: a tool result is a collapsed <details> that expands to the tool's raw output in both
// (the chat previously dropped that output, which is why this was extracted). The variants differ only where
// intended — the chat renders prose as Markdown, labels the agent "Analyst", and shows an assistant tool call
// as a live proposal ("wants to run X"); the transcript renders plain text, labels it "Agent", and shows the
// call after the fact ("ran X" with its args).
export type TurnVariant = 'chat' | 'transcript'

// showLabel renders a `show` (navigation) tool call as a human phrase — "opened finding <id>", "opened
// <path:line>", "opened <surface>" — for the live walkthrough trail (ADR-0053).
function showLabel(args: Record<string, unknown>): string {
  const kind = String(args?.kind ?? '')
  const id = String(args?.id ?? '')
  const loc = String(args?.location ?? '')
  if (kind === 'code') return `opened ${loc || id}`
  if (kind === 'surface') return `opened the ${id} view`
  return `opened ${kind}${id ? ` ${id}` : ''}`
}

export function MessageTurn({ m, variant }: { m: Msg; variant: TurnVariant }) {
  const chat = variant === 'chat'
  const cls = (base: string) => `${chat ? 'msg' : 'act-msg'} ${base}`
  const outCls = chat ? 'msg-toolout' : 'act-toolout'

  // A tool-result turn: its content is the tool's output or an error — collapsed by default, expandable so you
  // can read exactly what the tool returned to the agent.
  if (m.role === 'tool') {
    const label = m.content.startsWith('Tool ') ? m.content.split('\n')[0] : 'tool result'
    return (
      <details className={cls('tool') + (m.tool_error ? ' error' : '')}>
        <summary>🔧 {label}</summary>
        <pre className={outCls}>{m.content}</pre>
      </details>
    )
  }

  if (m.role === 'assistant') {
    // An assistant turn that requested a tool.
    if (m.tool_calls && m.tool_calls.length > 0) {
      const c = m.tool_calls[0]
      if (chat) {
        // Live co-drive: narrate what it's about to do (prose, if any) then the step chip; a `show` call is
        // navigation, so it reads as "opened X", not "wants to run".
        return (
          <div className={cls('propose')}>
            {m.content.trim() && <div className="propose-note"><Markdown source={m.content} /></div>}
            <div className="propose-tool">{c.tool === 'show' ? `📂 ${showLabel(c.args)}` : <>⚙ wants to run <b>{c.tool}</b></>}</div>
          </div>
        )
      }
      // Transcript: the call after the fact, expandable to its arguments.
      return (
        <details className={cls('propose')}>
          <summary>⚙ ran <b>{c.tool}</b></summary>
          <pre className={outCls}>{JSON.stringify(c.args ?? {}, null, 2)}</pre>
        </details>
      )
    }
    // An assistant turn with neither prose nor a tool call — the model returned an empty completion. In the
    // chat, show a clear placeholder rather than a silent blank bubble that reads as a hang.
    if (chat && !m.content.trim()) {
      return (
        <div className={cls('analyst') + ' empty'}>
          <b>Analyst</b>
          <div className="muted">(no response — ask again or rephrase)</div>
        </div>
      )
    }
    return (
      <div className={cls('analyst')}>
        <b>{chat ? 'Analyst' : 'Agent'}</b>
        {chat ? <div><Markdown source={m.content} /></div> : <div className="act-msg-body">{m.content}</div>}
      </div>
    )
  }

  // A user turn.
  return (
    <div className={cls('user')}>
      <b>You</b>
      {chat ? <div>{m.content}</div> : <div className="act-msg-body">{m.content}</div>}
    </div>
  )
}
