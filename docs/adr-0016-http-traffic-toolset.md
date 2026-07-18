# ADR-0016 — HTTP traffic toolset (Proxy · Replay · Intercept) + extensibility

Status: Accepted — fully delivered. Rename Repeater→Replay; Step 1 first-class history + exchange action
registry; Step 2 live-push (SSE hub); Step 3 Intercept (hold→edit→forward/drop, requests + responses);
Step 4 match/replace traffic-processor + scope highlighting. Plugin packaging of the seams is the remaining
future work (TODO.md).

## Context

The HTTP tools are meant to be daily drivers on par with an intercepting-proxy suite. Two problems to fix:

1. **Naming.** "Repeater" (and "Intruder", "Sequencer") are Burp's signature feature names; reusing them
   invites trademark friction and reads as a clone. We renamed **Repeater → Replay** (vendor-neutral, the
   term modern proxy suites use) across UI, CLI (`osb replay …`), the `pkg/replay` package, the exchange
   `origin` value (migration 0022), and audit actions (`replay.send`/`replay.blocked`). "Proxy" is generic
   plumbing and stays.
2. **The Proxy is an add-on, not a tool.** It's a live-polled flat list. It needs to become first-class
   (searchable history, detail view, send-to-Replay, save-as-evidence, intercept & edit) — and the whole
   toolset should be **structured so new tools and transforms are easy to add**, with a plugin system as an
   explicit future direction.

## Decision

Treat Proxy, Replay, and Intercept as **one HTTP traffic toolset over a shared substrate**, not three
unrelated tabs, and design **extension seams** in from the start.

### Shared substrate

All three operate on the same `http_exchange` (store + model) and the `pkg/proxy` / `pkg/replay`
transports. An exchange's `origin ∈ {proxy, replay}` records how it entered. This is what lets a captured
request flow into Replay and both flow into evidence with unbroken provenance (ADR-0002).

### Surfaces (workbench documents)

- **Proxy** — capture + searchable/filterable history (by host, method, status, path, origin, time).
- **Replay** — craft / edit / resend; **bindable to a methodology item** (ADR-0015 P3b), evidence
  auto-attaches.
- **Intercept** — hold → edit → forward/drop. New; needs an intercept queue + control channel in
  `pkg/proxy` (today it is passthrough capture only).

### Extension seams (the "easily extendable" requirement)

The toolset is built around four registries/interfaces so features slot in without touching the core:

1. **Exchange actions** — a named registry of things you can do to an exchange (*Send to Replay*, *Save as
   evidence → item*, *Copy as curl*, *Send to Analyst*, *Create finding*). The history/detail UIs render
   whatever is registered; a new action is one registration, not a UI edit.
2. **Traffic processors** — an ordered pipeline in `pkg/proxy` that sees each request/response
   (match-and-replace, tag, decorate, redact). The DLP egress monitor (ADR-0011) becomes the first
   first-party processor; new processors append to the pipeline.
3. **History columns & filters** — data-driven, so a new column/filter (including a plugin-provided one)
   is declarative.
4. **Tools as surfaces** — a new HTTP tool (e.g. *Compare*, *WebSocket*, a fuzzer named *Fuzz* — never
   "Intruder") is a new workbench document reusing the substrate + actions, not a rewrite.

### API

- Paginated/filtered `GET /v1/projects/{id}/exchanges?host=&method=&status=&origin=&q=&limit=&cursor=`
  (today it returns all).
- WebSocket/SSE live-push for new captures instead of 2.5s polling.
- Intercept control endpoints (list held, forward, drop, edit-then-forward).

### Plugin system — future direction (TODO, not built now)

Today the four seams are in-tree (Go interfaces + a React registry). The future plugin system exposes them
as **signed extension packages** (ADR-0003 format, ADR-0013 loader): third parties ship **traffic
processors, exchange actions, history columns, and whole tools** as governed, sandboxed plugins under the
same publisher-trust model as capabilities/methodologies — our vendor-neutral analog to proxy-suite plugin
ecosystems. Designing the seams as registries now is what makes that a later additive step rather than a
rewrite. Tracked in `TODO.md`.

## Step 3 — Intercept (design)

Capture is passthrough: `OnExchange` fires *after* the proxy has already forwarded. Intercept must
**pause a request in flight**, let the operator edit it, then **forward** (possibly edited) or **drop** it.
This is the one genuinely new subsystem, and it touches the proxy's hot path, so the design is explicit.

**Keep `pkg/proxy` generic — add one blocking hook.** Alongside `Allow` (scope) and `OnExchange`
(capture), the proxy gains an optional `Intercept` hook called at the single choke point both forwarding
paths already share (`handleHTTP` and `forwardTLS`), *after* the scope check and body read, *before*
`RoundTrip`:

```
type Decision struct { Drop bool; Method, URL, RequestHeaders, RequestBody string }
Intercept func(Held) Decision   // nil or disabled ⇒ forward unchanged, immediately
```

The proxy calls it, and either drops (return 403 to the client) or applies the returned method/url/
headers/body to the outgoing request. `pkg/proxy` knows nothing about queues or HTTP control — it just
respects the decision. This keeps the risky concurrency out of the transport code.

**The queue lives in the api layer (`pkg/api`), not the DB.** Held requests are in-flight and transient —
never persisted. An intercept manager holds, per project: an `enabled` flag and a map of `holdID → hold`,
where each hold carries the request snapshot and a `chan Decision`. The `Intercept` hook implementation:
if disabled, forward immediately; else register a hold, publish an `intercept.held` event (SSE), and block
on the hold's channel *selected against the request context* so a client disconnect auto-drops. When the
operator resolves, the control handler sends the decision on the channel and publishes `intercept.resolved`.
A forwarded request still flows through `OnExchange`, so it lands in history like any capture — one code
path, full provenance.

**Hold semantics (first-principles, matches operator expectations):**
- **Hold forever** until the operator acts (like every intercepting proxy) — no silent auto-forward.
- **Auto-drop on**: proxy stop (drain all holds), client/connection cancel (`ctx.Done()`), control-plane
  shutdown. No leaked goroutines: every blocked hook selects on its context and the manager's drain signal.
- **Requests *and* responses in the first pass.** Two choke points, one mechanism: the request hold sits
  before `RoundTrip`; the response hold sits after it, before the bytes reach the client. Each phase arms
  independently (`requests` / `responses` flags). When response interception is armed the proxy **buffers**
  the full upstream body so it can be shown and edited (streaming stays the default when it is off, so large
  downloads aren't buffered needlessly). The `Held`/`Decision` carry a `phase` and the fields relevant to
  it (method/url/headers/body for requests; status/headers/body for responses).
- **Governance unchanged and total**: out-of-scope hosts are already refused before a hold exists; DLP still
  inspects the *forwarded* (edited) request; **every hold, edit-and-forward, and drop is audited**. Editing
  a request is exactly the operator power a hands-on proxy grants — and here it is fully logged.

**Control protocol (API):**
- `PUT /v1/projects/{id}/intercept` `{enabled}` — arm/disarm; publishes state.
- `GET /v1/projects/{id}/intercept` — `{enabled, held:[…]}` for initial load (queue + arm state).
- `POST /v1/projects/{id}/intercept/{holdId}` `{action:"forward"|"drop", method?,url?,headers?,body?}` —
  resolve one hold, with optional edits on forward.
- Live: `intercept.held` / `intercept.resolved` / `intercept` (arm-state) events over the existing SSE hub
  (Step 2) — no new transport.

**Frontend — an Intercept surface** (a workbench document): an **armed** toggle, the held-request queue,
an editor for the currently-held request (method/url/headers/body), and **Forward** / **Drop** (Forward
carries edits). When armed, the operator works the queue; when off, traffic flows straight to capture. It
reuses the doc model and can offer the same exchange **actions** (e.g. Send to Replay) on a held request.

## Consequences

- **Extensibility is structural, not bolted on** — every planned Proxy feature is either an action, a
  processor, a column, or a surface, so the build itself exercises the seams.
- **Vendor-neutral naming** throughout; migration 0022 renames the stored `origin` value + constraint.
- **Intercept** is the one genuinely new subsystem (queue + control channel in `pkg/proxy`) and gets built
  behind the same scope-guard/DLP/audit as capture.
- Build order: (1) history UI + detail + paginated/filtered API + send-to-Replay + save-as-evidence over
  the action registry; (2) live-push; (3) intercept queue + Intercept surface; (4) match/replace processor
  + scope highlighting. Plugin packaging is a later, separate effort.

Relates to ADR-0007 (HTTP capture/Replay/sessions — supersedes its "Repeater" naming), ADR-0011 (DLP as a
traffic processor), ADR-0003/0013 (extension format + loader for the future plugin system), ADR-0015
(documents, Replay↔item binding).
