# ADR-0007 — HTTP capture, Repeater & interactive sessions

Status: Accepted — Repeater, Terminal (sandboxed container), and the intercepting Proxy shipped;
SSH-to-external-host and agent co-drive remain staged

## Context

Assessment work is driven daily by two surfaces the plan calls out as first-class: an HTTP
**Repeater** (craft a request, send it, read the response, tweak, resend, diff, save the
interesting one as evidence) and an interactive **terminal** to a host. Later, a full
intercepting **proxy** captures live traffic. All three touch the network against real targets, so
they inherit the scope guard (ADR — P6), the audit trail (ADR-0002), and eventually DLP (P10).

We build these in priority order — Repeater first (it needs no CA/TLS machinery), then the proxy,
then terminal/RDP sessions — behind a data model that all three feed.

## Decision

### HTTP exchanges

An **`http_exchange`** is the unit the Repeater and (later) the proxy both produce: a request and,
once sent, its response.

```
HTTPExchange{
  ID, ProjectID, Name,
  Method, URL, RequestHeaders (canonical text), RequestBody,
  Status, ResponseHeaders, ResponseBody, DurationMS,
  Origin (repeater | proxy),
  CreatedAt, SentAt
}
```

Request and response are stored as text on the row so the Repeater can edit and re-send them
cheaply. The exchange is anchored to a **project** (not an asset) because a request targets a URL,
and the project owns the scope allowlist that governs it. Bodies are size-capped; oversized bodies
are truncated with a marker rather than stored unbounded (CAS-backed large-body storage is a later
refinement).

### Sending is scope-guarded and audited

`pkg/repeater` owns the transport: `Send(ctx, Request) (Response, error)` issues one request with a
bounded timeout and no automatic redirect following (the operator sees each hop). It does **not**
enforce policy — the service layer does, so the same guard covers every caller:

1. Resolve the exchange's project scope allowlist and `scope.Check` the URL host. Out-of-scope →
   refused, nothing sent, the attempt audited (mirrors the task engine's scope block).
2. Send, capture status/headers/body/timing, persist onto the exchange, audit the send.

Unlike a capability, a Repeater send runs **in the control plane**, not a sandboxed runner: it is a
human-driven, single HTTP request with the response captured directly, and the same scope + audit
(+ future DLP) gates apply. The agent does **not** get an ungoverned send — if the Analyst is to
drive the Repeater later, it goes through the approval gate like any other gated tool.

### Save-as-evidence

Any exchange (or a selection within its response) can be promoted to evidence: the response bytes
are written to the CAS as an artifact and a **human-origin observation** (ADR-0005) is recorded
against it, so it flows through the same triage → finding path as tool output. Fragment-level
selection (a substring/header) is captured as the observation's location; byte-range evidence
entities remain future work (ADR-0002).

### Staged: proxy and sessions

- **Proxy** (later): an in-process intercepting proxy with a generated CA mints per-host certs,
  captures live request/response pairs as `http_exchange` rows with `origin = proxy`, and can hold a
  request for edit/forward. It reuses the exchange model, scope guard, and audit established here.
- **Interactive sessions** (later): a terminal (SSH/PTY) — and eventually RDP/VNC — opened through a
  runner. Every keystroke/command and the full transcript are logged to the append-only audit trail
  and are capturable as evidence; the agent may co-drive only through the approval gate, its input
  scope-checked and (future) DLP-scanned. Modeled as its own `session` entity when built.

## Consequences

- Repeater lands as a self-contained, immediately-useful slice with no TLS/CA prerequisites, while
  fixing the exchange schema the proxy will reuse — so the proxy is additive, not a rewrite.
- Scope enforcement gets a second call site (Repeater send) beyond the task engine; both share
  `pkg/scope`, keeping the guard in one place.
- Sending from the control plane is a deliberate exception to the sandbox-everything rule, bounded
  to human-driven single requests under scope + audit; agent-driven sends stay gated.

## Alternatives considered

- **Route Repeater sends through a sandboxed runner** (like capabilities): rejected for the
  interactive path — per-request container startup adds latency with no isolation benefit for a
  single outbound HTTP call the operator already authorized by scope. Revisit if a request must run
  from a project-bound runner's network vantage (then it becomes a capability).
- **Store every request/response in the CAS immediately**: rejected for the editable working set —
  exchanges are mutable drafts until sent and are cheap as rows; only evidence-promoted responses
  need immutable content-addressed storage.
