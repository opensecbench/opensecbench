# ADR-0025 — Egress via runner (Replay)

Status: Accepted — delivered (Phase 2a of the remote runner). A Replay send can go out from a chosen
enrolled runner's network vantage instead of the control-plane host. Extends ADR-0024's runner protocol
with an HTTP-request job; the MITM proxy (Phase 2b) is deferred.

## Context

ADR-0024 gave remote runners *execution*; network capabilities (nmap/http-probe) already egress from
inside their container, so they run from the runner's vantage for free. But the two **host-opened** HTTP
egress surfaces — Replay and the MITM proxy — still open their sockets on the control-plane host. Routing
**Replay** through a runner lets an operator (or the Analyst) send a crafted request *from inside a
client's network* or a different region/source IP — the core value for internal assessments and egress/
segmentation testing.

`replay.Client.Send(ctx, replay.Request) (replay.Response, error)` is the policy-free transport; both the
UI handler `sendExchange` and the Analyst `send_request` tool call it right after their own `scope.Check`.
The runner hop slots exactly at that call, so scope + audit stay control-plane-side, *before* egress.

## Decision

**HTTP-request job on the runner protocol.** `runnerhub.Dispatch` gains `Kind "http"` carrying an
`HTTPRequest{ID,Method,URL,Headers,Body}`; the runner returns an `HTTPResult{Status,Headers,Body,Error,
DurationMs}` via a new sig-authed `POST /v1/runners/http-result`. The broker mirrors the task path —
`DispatchHTTP` / `DeliverHTTP` / `ForgetHTTP` with a `pendingHTTP` map keyed by request id, and a runner
may only answer a request id dispatched to it (ownership check). The agent (`osb-runner`) performs the
request with a local `replay.New(0).Send` (reusing Replay's redirect/size policy) and posts the response.

**One shared selector.** `Server.egressSend(ctx, runnerID, replay.Request)`: `runnerID==""` → local
`replay.Send` (unchanged); else verify the runner is active + online and `DispatchHTTP`, blocking on the
result. A revoked/offline runner is an **error — no silent local fallback** (the operator chose the
runner). Both callers use it:
- `sendExchange` decodes an optional `{runner_id}` and audits `replay.send` with the egress.
- The Analyst `send_request` tool gains a `runner` arg; `ExecDeps.EgressSender` is wired to `egressSend`
  (falls back to local when unset). The gate + human approval are unchanged.

**Provenance.** `http_exchanges` gains an `egress` column (migration 0035): "" = control-plane host, else
the runner id that performed the send. Every sent exchange records the vantage it went out from —
real assessment provenance surfaced in the Replay UI ("via <runner>").

**Governance unchanged.** `scope.Check` runs control-plane-side before dispatch; the runner is only the
exit point. (DLP still wraps only the LLM path, as before — unchanged by this.)

## Consequences

- **Send from the runner's vantage.** Replay a crafted request from inside a client's network or another
  region — the headline value for internal/segmentation testing — with the response captured as usual.
- **Secret/credential material transits to the runner** in the request headers/body (over the operator's
  TLS/tunnel, per ADR-0024) — the runner is a trusted egress point; nothing new is persisted on it.
- **Both send paths covered** (UI + Analyst tool) through one selector, so behavior can't drift.
- **Provenance on every send.** The exchange records its egress vantage; audit records it too.
- **No fallback surprises.** Choosing an offline runner fails the send rather than quietly using the host.

## Out of scope (Phase 2b / later)
- Proxy MITM egress via runner: per-request forward, streaming + TLS over the outbound-connect tunnel —
  materially harder, deferred.
- A per-project/tool "default egress runner"; built-in TLS + cert-pinning; a fuller Runners settings UI.

Composes with ADR-0024 (extends its runner protocol/auth), ADR-0016 (the HTTP toolset), and ADR-0011
(scope + governance stay control-plane-side).
