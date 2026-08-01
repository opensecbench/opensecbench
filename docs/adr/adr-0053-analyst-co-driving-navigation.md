# ADR-0053 — Analyst co-driving: agent-navigable workbench

Status: Accepted — building (phased). The Analyst can take the human's workbench to a piece of evidence — open
a finding/observation/route/source file, or switch surfaces — through a read-only `show` tool, so a human and
the agent can walk through an assessment together over one shared view, **with the turn painting live** as it
works. Phase 1 (this ADR): navigation (the `show` tool, gated by a **Drive** toggle) **plus live-streaming
turns** — each step (tool call, navigation, explanation) appears in the chat as it happens instead of dumping
at the end. Mutating the UI (dismiss/promote/run) stays out of scope here and, when added, keeps the existing
approval gate.

**Live turns (added to phase 1).** The whole point — "take me to the finding and explain it" — falls flat if
the chat only paints when the entire multi-step turn finishes (a long investigation looked like a hang). So
`agent.Session` gained an `OnMessage` hook, fired as each message is produced; the analyst service publishes
those over the same SSE bus (`analyst.message`), and `AnalystPanel` appends them live while a send is in
flight, reconciling against the authoritative `refresh()` at turn end. The generalist persona now instructs
the agent to `show` evidence as it explains and to walk multi-part questions one item at a time. The
interactive step budget was also raised (8 → 40, `OSB_ANALYST_MAX_STEPS`) since real investigation needs it.

**Token streaming (added to phase 1).** The answer now types out token by token, not per-message. This is a
provider-layer capability, done generically: `llm.StreamingProvider` (optional interface) + `llm.Stream()`,
which streams real deltas when the provider implements it and otherwise falls back to `Complete` + one
whole-text delta — so it works across *every* provider. `AnthropicProvider` and `OpenAIProvider` (covering
OpenAI/DeepSeek/Grok/Ollama/Azure) implement it by parsing their SSE wire formats via a shared `sseData`
reader; Bedrock, the prompted wrapper, and the mock fall back cleanly. `Session.OnDelta` → `analyst.delta`
SSE → `AnalystPanel` accumulates into a live-typing bubble that finalizes when the turn's `analyst.message`
arrives. (Fall-through chains via `FallbackProvider` don't stream yet — they use the whole-text fallback.)

## Context

The Analyst is a docked chat (`AnalystPanel`, ADR-0015) with a governed toolset (ADR-0017) that reads the
assessment corpus and drives capabilities. Two gaps surfaced while using it to review a single finding:

1. The agent has **no idea what the human is looking at** — asked "is this finding vulnerable?", it had to
   `list_findings`, found two, and asked "which one?".
2. The agent can **describe** where evidence lives but can't **take you there**; the human re-navigates by hand.

Yet the frontend already has both halves of the channel this needs. Backend→frontend push exists: the
per-project SSE bus (`pkg/events` → `GET /v1/projects/{id}/events` → `api.subscribeProjectEvents`), today
carrying `exchange`/`proxy`/`intercept`. And navigation is already a set of functions in the Workbench —
`openFinding`, `openCodeFile`, `navigateTo`, `activateSurface` — reused across surfaces and the ADR-0050
click-to-file jumps. So agent navigation is mostly *exposing existing handlers to the agent*, not new view code.

This realizes the long-standing "human and agent both drive the process" direction (agent-architecture notes):
the workbench becomes a shared surface, not a thing only the human touches.

## Decision

**1. A read-only `show` tool.** `show(kind, id?, location?)` where `kind ∈ {finding, observation, route,
code, surface}`. It changes no data. Handled at the service level (like `delegate`, `pkg/analyst/service.go`)
because it is a UI side effect, not a store query: it builds a `UICommand{action:"show", kind, id, location}`
and hands it to an injected publisher. It validates args (e.g. `code` needs `id`+`location`) before publishing
and always returns success, so the agent keeps explaining whether or not the human is currently letting it drive.

**2. Delivery over the existing event bus.** The API wires the publisher to
`events.Publish(Event{Type:"ui.command", ProjectID, Payload: cmd})` (`analystService()`), reusing the SSE
stream — no new transport. The Workbench subscribes and applies commands through the same
`openFinding`/`openCodeFile`/`navigateTo`/`activateSurface` handlers a human click uses, so agent navigation
and human navigation are literally the same code path. Unknown targets are a no-op.

**3. Governance = an explicit Drive toggle, not per-action prompts.** A **Drive** toggle in the Analyst header
(off by default, persisted in `localStorage`) is the consent. Off: the agent can call `show` and *suggest*, but
the Workbench ignores its commands. On: the agent moves the view freely. Navigation is reversible and low-stakes,
so the toggle *is* the authorization — no approval card per hop. The human's own clicks always win, and toggling
off takes back the wheel instantly. This is deliberately lighter than the ADR-0019 trust curve, which stays the
governor for **mutations**.

**5. Capability parity (landed).** Principle: the agent and the human share the same actions — a hardcoded
"only a human can do X" breaks it. The agent had *no* tool for a finding's status, so it punted ("you'll have
to close it manually"). Fixed with `set_finding_status` (open/confirmed/remediated/accepted/false_positive +
note), the same control the finding page's dropdown gives a human. It is NOT gated by default — like
`triage_observation` (which already dispositions observations directly), not like `create_finding`. Governance
is a *separate, configurable* layer: the trust-curve approval policy (ADR-0019) can gate it if the human wants
oversight — parity of capability, with a tunable governor, never a wall. (Whether the other still-gated
mutations — create_finding, send_request — should also default to parity is an open question; send_request's
gate guards outbound/egress, a real safety reason, so it's not purely symmetric.)

**4. Awareness (landed).** Each chat message now carries a short `view_context` describing what's on screen —
the active document decides it (`Workbench.describeView`: the open finding/observation/code/surface, e.g.
`the finding "Zip Bomb DoS" (id …)`). `Send` prepends it to that turn's user message as an LLM-only annotation
(the fresh `prior` copy is edited; the persisted user turn stays clean), so "explain this" / "is this
exploitable?" resolves to the on-screen subject without the agent enumerating everything. Coarse for list
surfaces (no per-row selection yet); precise for a finding/code document.

## Consequences

- **Walkthroughs work at chat pace.** "Take me through the reachable findings" = the agent narrates and opens
  each as it goes. A companion **anchored-refs** layer (phase 2) will let replies carry `osb://finding/…`,
  `osb://code/asset/path:line` links the human clicks — one reply covers a whole walkthrough with no extra agent
  round-trips, and it reuses the same dispatcher and ADR-0050 jumps.
- **Safe by construction.** `show` is read-only and auto-approved; the worst an agent can do with Drive on is
  move your view, which you see and can override. No mutation path is opened here.
- **Least-privilege intact.** `show` joins the tool catalog; full-catalog profiles (the interactive generalist)
  get it. Explicit-toolset profiles opt in by listing it.
- **Follow-ups:** on-screen awareness in the message; anchored `osb://` refs (phase 2); gated UI *actions*
  — dismiss/promote/run — behind Drive **and** the approval card (phase 3); a shared single SSE subscription
  (the Workbench now opens its own alongside Intercept/Proxy).
