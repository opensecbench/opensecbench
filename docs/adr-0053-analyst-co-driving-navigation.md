# ADR-0053 — Analyst co-driving: agent-navigable workbench

Status: Accepted — building (phased). The Analyst can take the human's workbench to a piece of evidence — open
a finding/observation/route/source file, or switch surfaces — through a read-only `show` tool, so a human and
the agent can walk through an assessment together over one shared view. Phase 1 (this ADR): navigation only,
gated by an explicit **Drive** toggle. Mutating the UI (dismiss/promote/run) stays out of scope here and, when
added, keeps the existing approval gate.

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

**4. Awareness is deferred, not folded in here.** Passing "what's on screen" into the message (so "this finding"
resolves) is a natural companion but a separate change to the message path; tracked as follow-up.

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
