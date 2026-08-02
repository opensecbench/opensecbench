# ADR-0061 — Local API security model

Status: Accepted. The loopback control-plane API is hardened with three layered local defences —
a `Host`-header allowlist, an `Origin` allowlist, and a per-instance bearer token stored `0600` in
the data dir — so it is not driven by a browser page the user visits or by another OS user on a
shared host. This amends ADR-0001, which deferred all API authentication to a "future team service".

## Context

ADR-0001 established a headless Go control plane exposing a **local HTTP API on loopback**, with
every client (Wails desktop, `osb` CLI, future web/team client) a thin HTTP client against it. Its
final consequence line explicitly punted auth: *"Loopback-only binding for the API in single-user
mode; authentication/transport hardening is a concern for the future team service, not the local
daemon."*

For a tool that holds client assessment data, "loopback is enough" turns out to be weaker than it
reads, in three concrete ways:

1. **Browser cross-origin reads.** `withCORS` reflected *any* `Origin` back as
   `Access-Control-Allow-Origin`, and no handler validated the `Host` header. So any web page the
   user visits in a normal browser could `fetch('http://127.0.0.1:7373/v1/...')` and **read the
   responses** — enumerate projects, read findings, trigger scans, drive the proxy. Combined with
   DNS rebinding (attacker domain re-resolved to `127.0.0.1`, so the browser sends
   `Host: attacker.com`), the loopback bind alone does not stop a remote attacker who can get a tab
   open on the machine.
2. **Other OS users.** Loopback is **not user-scoped**: on a multi-user host, any other user's
   process can connect to `127.0.0.1:7373`. Nothing gated that.
3. **No client auth primitive at all**, which also blocks the planned `osb` CLI — a second local
   client needs a way to prove it's allowed to talk to the daemon.

The remote-runner listener (ADR-0024) already has strong auth (ed25519, one-time enrollment) and is
off by default; it is out of scope here. This ADR concerns only the main loopback API + SSE + the
session WebSocket.

## Decision

Add three layered defences to the main API, applied as a single middleware wrapping the mux
(`Server.Handler`). Together they make the local trust boundary the **OS user account**, which is
the correct boundary for a single-user local workbench.

**1. `Host`-header allowlist (anti-DNS-rebinding).** Every request's `Host` must resolve to a
loopback name — `127.0.0.1`, `::1`, `localhost`, or any IP that `net.IP.IsLoopback()` accepts.
Anything else (a LAN IP, an attacker domain) is rejected `403`. A rebinding attack cannot forge a
loopback `Host`, because the browser sets `Host` to the name it dialled.

**2. `Origin` allowlist (browser cross-origin).** CORS reflects `Access-Control-Allow-Origin` **only**
for allowlisted origins: absent/`null`, any loopback origin, and the Wails webview origin
(`wails://…`, `*.localhost`). For every other origin, no `Access-Control-Allow-*` header is set, so
the browser blocks the response read. This deliberately errs toward *not* breaking the desktop
webview across platforms rather than a strict single-origin lock.

**3. Per-instance bearer token (client auth + other-user defence).** On startup the control plane
loads or creates `<data-dir>/api-token` — 32 bytes of `crypto/rand`, hex-encoded, written **`0600`**
via a temp-file-and-rename so it is never briefly world-readable. The token is **persistent**: it is
reused across restarts and rotated only by deleting the file (or the file being absent), per the
product decision to keep long-lived local scripts working without re-reading on every daemon bounce.
Every request except CORS preflight (`OPTIONS`) and the liveness probes (`/healthz`, `/readyz`) must
present the token, compared in constant time (`crypto/subtle`):

- **Header:** `Authorization: Bearer <token>` — used by all `fetch`/XHR clients and the CLI.
- **Query fallback:** `?token=<token>` — for contexts that *cannot* set a header: `EventSource`
  (SSE), the browser `WebSocket` API, `<img>/<a>` URLs, and pages the desktop app opens in the
  **system browser** via `App.OpenURL` (reports, transcripts, downloads). This mirrors Jupyter's
  `?token=`; acceptable because the listener is loopback and logs are local.

**Enforcement is gated on a token being configured.** When `authToken == ""` the middleware enforces
neither the token nor the `Host` check (CORS tightening always applies). The control plane always
sets a token, so the desktop app and headless daemon are always protected; unit tests that construct
`api.New` without one keep running unauthenticated. This is an internal switch, **not** an operator
"disable auth" flag — there is no env var or option to turn auth off on a real instance.

**Client token acquisition (the multi-client contract):**

- **Desktop webview** — cannot read files (sandboxed JS), so the Go side hands the token over the
  Wails bridge via a new bound method `App.APIToken()`. The frontend fetches it once during boot
  (`initAuth()` before React renders) and attaches it thereafter.
- **`osb` CLI and any future local client** — read `<data-dir>/api-token` directly. The path is
  `os.UserConfigDir()/opensecbench/api-token` (beside `opensecbench.db`), exported as
  `controlplane.APITokenPath(dir)`. Filesystem ownership (`0600`) *is* the authentication: a client
  that can read the file is already running as the daemon's user.
- **Browser dev** (`vite` pointed at a running daemon, no Wails bridge) — falls back to
  `VITE_OSB_TOKEN`, alongside the existing `VITE_OSB_API`.

## Consequences

- The API is no longer reachable by a random web page the user visits, by DNS rebinding, or by a
  different OS user on the same host. The effective trust boundary is now "can read files owned by
  this user" — the same privilege the daemon itself runs at, which is the right bar for a local tool.
- **A process running as the same user can still read the token file and call the API.** That is not
  a regression and not defended here: such a process already has the user's full privileges. This
  ADR is about *other* users and *browsers*, not same-user malware.
- The token is **not** confidentiality over the wire — traffic is plaintext HTTP on loopback, which
  never leaves the host. Transport encryption remains the future team service's concern (ADR-0001,
  ADR-0024), unchanged.
- Tokens appear in `?token=` URLs for SSE/WS/opened pages, so they can land in local process/HTTP
  logs. Accepted: loopback-only, single-user, local logs. If we later add request logging that ships
  off-box, the query token must be redacted.
- The `osb` CLI (not yet built) now has a defined, zero-handshake way in: read the file, send the
  header. No enrollment, unlike the remote runner.
- Persistent (vs per-launch) token means a leaked token stays valid until the file is deleted;
  rotation is a manual `rm <data-dir>/api-token` + restart. A future `osb auth reset` can automate it.
- Existing `pkg/api` unit tests are unaffected (they build `api.New` with no token → auth disabled).
  The three live-instance tests in `controlplane_test.go` now send `cp.Token`.
