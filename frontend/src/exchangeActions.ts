// Exchange actions — the first extension seam of the HTTP traffic toolset (ADR-0016).
//
// Anything you can *do* to an HTTP exchange (from Proxy history, Replay, a detail view) is a registered
// action, not hard-coded UI. Adding a capability = appending one entry here; a future plugin system
// (TODO) will let extension packages contribute actions the same way. The UI renders whatever is
// registered and enabled for a given exchange.

import { HTTPExchange } from './api'

// ActionContext is the toolset capability surface an action may use. Kept small and explicit so
// actions stay declarative and testable, and so plugin-provided actions get a stable contract.
export interface ActionContext {
  openReplay: (ex: HTTPExchange) => void
  saveEvidence: (ex: HTTPExchange) => Promise<void>
  copy: (text: string) => void
  notify: (message: string) => void
}

export interface ExchangeAction {
  id: string
  label: string
  icon?: string
  /** Hidden entirely when this returns false (default: always shown). */
  enabled?: (ex: HTTPExchange) => boolean
  run: (ex: HTTPExchange, ctx: ActionContext) => void | Promise<void>
}

/** Render an exchange's request as a copy-pasteable curl command. */
export function toCurl(ex: HTTPExchange): string {
  const parts = [`curl -i -X ${ex.method} ${shellQuote(ex.url)}`]
  for (const line of ex.request_headers.split('\n')) {
    const h = line.trim()
    if (h) parts.push(`  -H ${shellQuote(h)}`)
  }
  if (ex.request_body) parts.push(`  --data ${shellQuote(ex.request_body)}`)
  return parts.join(' \\\n')
}

function shellQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`
}

export const EXCHANGE_ACTIONS: ExchangeAction[] = [
  {
    id: 'send-to-replay',
    label: 'Send to Replay',
    icon: '↔',
    run: (ex, ctx) => ctx.openReplay(ex),
  },
  {
    id: 'save-evidence',
    label: 'Save as evidence',
    icon: '🔬',
    enabled: (ex) => !!ex.sent_at, // only a completed exchange has a response to preserve
    run: async (ex, ctx) => {
      await ctx.saveEvidence(ex)
      ctx.notify('Saved as evidence')
    },
  },
  {
    id: 'copy-curl',
    label: 'Copy as curl',
    icon: '⧉',
    run: (ex, ctx) => {
      ctx.copy(toCurl(ex))
      ctx.notify('Copied curl command')
    },
  },
]

/** Actions applicable to a given exchange, in registry order. */
export function actionsFor(ex: HTTPExchange): ExchangeAction[] {
  return EXCHANGE_ACTIONS.filter((a) => !a.enabled || a.enabled(ex))
}
