# ADR-0026 — Streaming runner tunnel + proxy egress via runner

Status: Accepted — delivered (Phase 2b of the remote runner). The MITM proxy can forward every request
through a chosen runner's network vantage, with responses **streaming** (not buffered) over a multiplexed
tunnel. Completes "egress via runner" for the HTTP toolset; capability + Replay egress were ADR-0024/0025.

## Context

The proxy was the last host-opened egress surface: each forwarded request went out via
`p.transport.RoundTrip(outReq)` on the control-plane host. Routing it through a runner lets proxied traffic
originate from inside a client's network / a different region while the proxy still terminates TLS and
inspects/captures plaintext. The runner is outbound-connect (no inbound ports), and we require **streaming**
forwarding (large downloads must not buffer/cap). The buffered Phase-2a HTTP job can't stream, so this
builds a small multiplexed tunnel over the runner connection. A key narrowing: the proxy already fully
reads the *request* body before forwarding (for rules/capture), so only the **response body** must stream.

## Decision

**A multiplexed, flow-controlled tunnel** (`pkg/runnertunnel`) over one WebSocket per runner (gorilla, as
in `pkg/api/sessions.go`). The runner dials `GET /v1/runners/tunnel` (ed25519-authed by the existing
`runnerAuth` middleware, then upgraded). Frames are WS binary messages `[streamID][type][payload]` with
types OPEN/DATA/WINDOW/EOF/RESET. **Credit-based flow control** (a receiver grants window as it consumes)
keeps the shared reader draining the socket into bounded per-stream buffers — no head-of-line blocking, no
unbounded buffering. The control plane is the stream **initiator** (`Session.Open` per forward); the runner
**accepts**. `Stream` is an `io.ReadWriteCloser` carrying its OPEN metadata.

**Proxy forwarder.** `Proxy.transport` is widened from `*http.Transport` to `http.RoundTripper`; `proxy.New`
gains a `forward` param (nil = local transport, unchanged). The two seam lines stay
`p.transport.RoundTrip(outReq)`. The runner forwarder (api-level, `tunnelForwarder`) opens a stream with
OPEN meta `{method, url, header, contentLength, insecure}`, streams the request body up + half-closes, then
returns an `*http.Response` read via `http.ReadResponse` over the stream — so the **response body streams**
to the client (tee'd to the bounded capture copy, exactly as before). The runner agent's tunnel loop builds
the request from the meta (streaming request body from the stream), performs it with an `http.Client`
(`InsecureSkipVerify` per the flag, matching the local proxy transport), and `resp.Write`s the response
back down the stream.

**Selection + provenance.** Egress is **per-proxy-session**: `startProxy` takes `runner_id`; the runner
must be active and have a **live tunnel** (else 502 — no silent local fallback). `liveProxy`/`proxyStatus`
expose it, and `proxyCapture` records `egress = runnerID` (the ADR-0025 column) so proxy history shows the
vantage. Scope/`allow` gate is unchanged — control-plane-side, before dispatch.

## Consequences

- **Proxy from the runner's vantage, streaming.** Browse a target through the proxy and it egresses from the
  runner's IP; large downloads stream (proven end-to-end with an 8 MiB body, larger than any buffer cap).
- **Inspection preserved.** The proxy still MITM-decodes and captures plaintext; only the upstream hop moves
  to the runner (the runner sees the decoded request, does its own TLS to the target).
- **Governance unchanged.** Scope enforced before forward; captures record the egress runner.
- **Two runner connections.** The agent holds both its SSE dispatch stream (tasks + buffered HTTP job) and
  the WS tunnel (proxy streams). Unifying them onto one transport is a future cleanup.

## Out of scope / limitations
- Non-HTTP CONNECT passthrough / raw-TCP tunneling (the proxy always MITM-decodes; only decoded HTTP is
  tunneled). WebSocket-upgrade proxying (not tunneled today either).
- The mux is a minimal credit-windowed protocol, not a hardened yamux; one tunnel per runner. Header
  ordering/casing goes through the proxy's existing `formatHeaders` string form (same as its intercept/
  rules/capture paths). Built-in TLS + cert-pinning still deferred to the operator (ADR-0024).
- Replay stays on the buffered Phase-2a HTTP job (bounded request/response — no need to stream).

Composes with ADR-0024 (runner protocol/auth), ADR-0025 (egress provenance column), and ADR-0016 (the
HTTP toolset).
