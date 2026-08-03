# ADR-0024 — Remote runner (outbound-connect)

Status: Accepted — Phase 1 delivered. A runner agent dials home to the control plane, authenticates with
an ed25519 key established at enrollment, and executes dispatched capability tasks from its own network
vantage. Realizes the additive remote `Runner` implementation ADR-0004 anticipated.

## Context

Every capability ran on the control-plane host's Docker (`runner.LocalRunner`). Real assessment work needs
to run *from somewhere else* — inside a client's network, behind NAT, or from a different region/source
IP. ADR-0004 built the `Runner` interface transport-agnostic for exactly this ("a remote outbound-connect
runner is an additive implementation… the task/artifact model does not change").

A decisive property: **network capabilities (nmap, http-probe) do the outbound connect inside the
container** (`docker run --network bridge`, target in argv). So running them on a remote runner *is*
egress-from-that-runner's-vantage — the headline value — with no separate egress selector and no source
mount. Phase 1 therefore covers **network capabilities**; routing Replay/proxy's host-opened HTTP sockets
through a runner (a per-request forward hop) is a distinct Phase 2.

## Decision

**Trust model.** The main API stays loopback-only (ADR-0001). A **separate listener** (`--runner-addr`,
off by default) serves only `/v1/runners/{enroll,stream,result}`, so the network-exposed surface is just
the authenticated runner protocol; the operator fronts it with TLS or a tunnel (how assessment runners
deploy anyway). Every runner request is authenticated by an **ed25519 signature** over
`method|path|timestamp|nonce|sha256(body)`, verified against the runner's enrolled public key, with a 60s
window bounding validity and a server-side per-runner nonce cache rejecting any signature replayed inside
that window (so an on-path attacker cannot re-open the dispatch stream and siphon a runner's secret env).
Operator actions (mint token, list, revoke) stay on the trusted loopback API.

**Enrollment.** The operator mints a one-time token (`osb runner enroll-token`); only its sha256 is stored
(never the token), consumed atomically at enroll. The runner (`osb-runner --enroll <token>`) generates an
ed25519 keypair, posts its public key, and persists `{runner_id, privKey}` locally (0600). The control
plane records it in a `runners` table.

**Dispatch (runner dials home, HTTP-only for reach).** `GET /v1/runners/stream` is the runner's
downstream SSE channel (dispatch + cancel events, 25s heartbeat that also refreshes `last_seen`);
`POST /v1/runners/result` returns a task's output. A control-plane broker (`pkg/runnerhub`) holds each
runner's stream and the pending result waiters; `remoteRunner` adapts a hub-connected runner to
`runner.Runner`, so the durable task engine dispatches to it through the same one-line seam as
`LocalRunner`.

**Engine integration.** `RunRequest.RunnerID` (persisted as `tasks.runner_target`, restored on durable
reconstruction) selects the runner; a resolver maps it to the hub-connected runner, failing the task
cleanly if the runner is revoked or offline. **Scope is enforced control-plane-side before dispatch**, so
governance is unchanged. A remote task interrupted by a control-plane restart is requeued (ADR-0023) and
re-dispatched.

## Consequences

- **Run from the runner's vantage.** nmap/http-probe on a remote runner scan/probe from that runner's
  network — inside a client's environment, or a different region/source IP — the core assessment value.
- **Governance stays put.** Scope + audit are control-plane-side, before any dispatch; the runner is only
  the exit point. (DLP today wraps only the LLM path, unchanged by this.)
- **Secret values transit to the runner.** A capability's resolved `SecretEnv` is part of the dispatched
  RunSpec, so secret *values* reach the runner over the operator's secure transport (never persisted on
  the runner, injected by name into its container, never on argv — ADR-0011). Documented, deliberate.
- **Authenticated, minimal exposure.** Only the ed25519-authenticated runner subset is network-reachable;
  the main API stays loopback. Transport confidentiality is the operator's TLS/tunnel (ADR-0001 posture).
- **Durable + resumable.** Remote tasks are ordinary durable-queue tasks; a restart re-dispatches them.

## Out of scope (follow-ups)
- Phase 2: routing Replay/proxy host egress through a runner (per-request forward hop).
- Source-scanning capabilities on remote runners (shipping the `/src` tree). Built-in TLS + cert-pinning
  (operator TLS for now). A repo split of `osb-runner`. A Settings "Runners" UI (CLI + API only in Phase 1).

Composes with ADR-0004 (this is the anticipated remote `Runner`), ADR-0023 (remote tasks are durable), and
ADR-0011 (secret values never persisted; injected by name).
