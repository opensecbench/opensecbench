# OpenSecBench

An open, free, local-first **security assessment workbench** — a unified environment for
planning and conducting application security assessments: executable methodologies, distributed
tool runners, evidence management with full provenance, and a multi-agent AI assistant (the
**Analyst**), across the full lifecycle of an assessment.

OpenSecBench is not a scanner aggregator, a vulnerability-management system, or an autonomous
pentester. It is a working environment that makes skilled AppSec and assessment engineers
dramatically more effective — and that a community can extend and share.

> **Status: pre-release, under active development.** The local single-user workbench is broadly
> feature-complete — the assessment lifecycle, sandboxed capabilities, the evidence loop, the HTTP
> toolset, the multi-agent Analyst, methodology/coverage, the knowledge base, reporting, and signed
> extensions all work. Platform-reach items (remote runners, hosted hub/team services) are still in
> progress. The repository is private during the build and will be open-sourced at release.

## License

Licensed under the [Apache License 2.0](LICENSE).

## What's inside

An IDE-style workbench shell with an always-present AI dock:

- **Projects, applications, assets, durable targets** — an asset taxonomy with sensitivity levels; a
  durable `target` that carries a knowledge base across engagements; project templates; omni-search
  across everything.
- **Sandboxed capabilities & runners** — every security operation runs in an isolated Docker runner
  with resource limits and scope enforcement. Built-ins: source inventory, Semgrep, TruffleHog,
  Grype, Syft, nmap, HTTP probe — plus anything shipped as an extension.
- **Evidence loop** — tool output is deterministically interpreted (SARIF, nmap, TruffleHog) into
  **observations** with full provenance; triage and promote to **findings** — only confirmed
  observations can back a finding.
- **HTTP toolset** — an intercepting **Proxy** (capture, history, match/replace), **Replay**
  (edit/send/diff, save-as-evidence, scope-guarded), and **Intercept** (hold → edit → forward/drop).
- **The Analyst (multi-agent)** — least-privilege specialist agents (Lead, Code Analysis, Vuln
  Validator, Pentester, Triage, Report Writer — plus your own custom ones), a **Lead** that delegates,
  and **playbooks** that run as dependency-ordered plans you can trigger, **schedule**, record from a
  run, or build from scratch. Agents read the whole evidence corpus (code, documents, correspondence,
  traffic), share a workspace, and run sandboxed code — all behind a **trust-curve approval policy**
  over a fixed scope + data-egress floor.
- **Methodology & coverage** — adoptable methodologies; coverage roll-up tied to evidence.
- **Knowledge base** — durable, target-anchored, inherited across engagements; feeds agent context
  and methodology suggestions.
- **Reporting & visualization** — multi-type reports (executive · technical · retest · compliance ·
  branded) in MD/HTML/PDF/DOCX with embedded figures, and a **graph** view of the assessment.
- **Governance & provenance** — an append-only, hash-chained **audit trail**; an encrypted **secrets
  vault** with exec-time injection, output redaction, and a **DLP** egress monitor; a **scope guard**
  allowlist; policy profiles.
- **Extensions** — capabilities, methodologies, and report templates ship as **signed, digest-pinned**
  open-format packages; a community hub (a static, signed index) to browse, publish, and install with
  explicit trust.
- **Collaboration** — portable, encrypted project export/import with signing.

## Architecture (at a glance)

- **Go control plane** — a standalone headless daemon exposing a local HTTP API. All clients
  (desktop, CLI, future web) are thin clients against it; domain logic never depends on the
  desktop framework.
- **Wails + React desktop** — the primary client; boots the control plane in-process. The same
  frontend runs in a browser against the daemon.
- **SQLite** for structured project data; **content-addressed storage** for immutable artifacts.
- **OCI-sandboxed capabilities** — every security operation runs in an isolated runner.
- **Own agent runtime** — the control plane owns the tool-calling loop; providers are inference-only
  and swappable: **Anthropic**, **OpenAI-compatible** (incl. Azure, DeepSeek, xAI Grok), **local
  models** (Ollama), and the **Claude CLI** (subscription). Tool use is first-class — native tool-use
  where the backend supports it, a prompted fallback otherwise.

Everything — capabilities, methodologies, project templates, playbooks, report templates — is a
versioned, signed, open-format **extension package**.

See [`docs/`](docs/) for architecture decision records and format specs, and
[`TASKS.md`](TASKS.md) / [`TODO.md`](TODO.md) for current work and backlog.

## Repository layout

```
main.go     desktop app entrypoint (behind the `desktop` build tag)
cmd/        headless entrypoints: daemon (control-plane API), osb (CLI)
pkg/        control-plane packages (see docs/adr-0001-architecture-overview.md)
extensions/ first-party packages (same format as third-party)
images/     OSB-built container images (e.g. the sandboxed claude-cli)
frontend/   React + TypeScript desktop/web UI
docs/       architecture decision records + open-format specs
migrations/ SQLite schema migrations
```

## Development

Requires the Go toolchain declared in `go.mod` (auto-managed by `GOTOOLCHAIN`).

```sh
go build ./...      # core packages (the desktop app is excluded — see below)
go test ./...
```

**Headless control plane + CLI:**

```sh
go run ./cmd/daemon                       # serves the API on 127.0.0.1:7373
go run ./cmd/osb project create --name X  # thin client against the API
go run ./cmd/osb project list
```

**Frontend in a browser** (no desktop toolchain needed):

```sh
go run ./cmd/daemon                          # in one terminal
cd frontend && npm install && npm run dev    # in another → http://localhost:5173
```

**Desktop app (Wails):** boots the control plane in-process and renders the frontend in a native
window. Needs the [Wails](https://wails.io) CLI and, on Linux, the GTK/WebKit dev libraries
(`libgtk-3-dev`, `libwebkit2gtk-4.1-dev`). The desktop entrypoint (`main.go`) is behind the
`desktop` build tag so it never affects `go build ./...` or CI — **you must pass the tag to Wails**
(the Makefile does it for you):

```sh
make dev                                 # = wails dev -tags "desktop webkit2_41"
make build                               # = wails build -tags "desktop webkit2_41"
OSB_LLM_PROVIDER=claude-cli make dev     # with the Analyst enabled
```

Notes:
- Without the `desktop` tag, Wails builds a stub that prints a hint and exits.
- On Ubuntu/Pop!_OS **24.04+**, webkit is 4.1, so the `webkit2_41` tag is required (the Makefile
  includes it). On older distros with webkit2gtk-4.0, use `make dev WAILS_TAGS=desktop`.

## The Analyst (AI)

The control plane owns the agent loop; providers are inference-only (ADR-0006/0017). Configure one via
`OSB_LLM_*` when starting the daemon (in production, keys come from the encrypted vault — env is a dev
convenience):

```sh
# local Ollama, no key, no egress (MODEL must be one you have pulled):
OSB_LLM_PROVIDER=ollama OSB_LLM_MODEL=qwen2.5 go run ./cmd/daemon
# remote/non-default Ollama — set the OpenAI-compatible base URL (note the /v1):
OSB_LLM_PROVIDER=ollama OSB_LLM_BASE_URL=http://10.0.0.5:11434/v1 OSB_LLM_MODEL=qwen2.5 go run ./cmd/daemon
# hosted, OpenAI-compatible:
OSB_LLM_PROVIDER=deepseek OSB_LLM_API_KEY=... go run ./cmd/daemon
OSB_LLM_PROVIDER=grok     OSB_LLM_API_KEY=... go run ./cmd/daemon
# Claude via the CLI (uses your local subscription login):
OSB_LLM_PROVIDER=claude-cli go run ./cmd/daemon
# Anthropic API:
OSB_LLM_PROVIDER=anthropic OSB_LLM_API_KEY=... go run ./cmd/daemon

osb analyst ask "how many findings are there, and which are high severity?"
```

Providers: `ollama`, `deepseek`, `grok`, `openai`/`azure` (set `OSB_LLM_BASE_URL`), `anthropic`,
`claude-cli`. In the desktop app, providers, the trust-curve approval policy, and custom agents are all
configurable from the Analyst settings; playbooks are triggered, scheduled, and built from the **Agents**
surface. The Analyst calls read tools over your data (auto-approved) and gates outbound/mutating actions
through an **approval queue** (`osb approval list|approve|deny`).

Governance & agent env (dev convenience; the desktop UI exposes the same controls):

- `OSB_EGRESS_POLICY` — `strict` (default) blocks sending a **private** asset's contents (a capability's
  output, or its source/documents read directly) to an **external** provider; `open` allows it. Local
  providers (Ollama) are never blocked.
- `OSB_AGENT_MAX_TOKENS` — per-turn token budget (default 200000); the run stops if exceeded.
- `OSB_AGENT_MAX_CONCURRENT` — cap on concurrent sub-agents (default 4).
- `OSB_LLM_NATIVE_TOOLS` — native tool-use is on by default for capable backends; set `0` to force the
  prompted fallback.
- `OSB_LLM_CLI_SANDBOX=1` (+ `OSB_LLM_CLI_IMAGE`) — run `claude-cli` inside a runner container that mounts
  only `~/.claude/.credentials.json`, instead of on the host (build the image with `make claude-image`).
- `OSB_WORKSPACE_DIR` — root for the per-project agent workspace (defaults beside the database).
