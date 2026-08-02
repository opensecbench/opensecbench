# OpenSecBench

An open, free, local-first **security assessment workbench** — a unified environment for
planning and conducting application security assessments: executable checklists, distributed
tool runners, evidence management with full provenance, and a multi-agent AI assistant (the
**Analyst**), across the full lifecycle of an assessment.

OpenSecBench is not a scanner aggregator, a vulnerability-management system, or an autonomous
pentester. It is a working environment that makes skilled AppSec and assessment engineers
dramatically more effective — and that a community can extend and share.

<!-- TODO(screenshot): add a workbench screenshot before flipping the repo public.
     Suggested shot: the full IDE workbench with the Analyst dock open on a real assessment.
     Drop the image in docs/images/ (NOT the top-level images/, which holds container builds) and
     replace this comment with, e.g.:
     ![The OpenSecBench workbench](docs/images/workbench.png) -->

> **Status: early access, under active development.** The local single-user workbench is broadly
> functional — the assessment lifecycle, sandboxed capabilities, the evidence loop, the HTTP
> toolset, the multi-agent Analyst, checklists and coverage tracking, the knowledge base, reporting,
> and signed extensions all work. Platform-reach items (remote runners, hosted hub/team services) are
> still in progress.
>
> This is **pre-1.0 and moving fast**: it's a work in progress, and interfaces, data formats, and
> features can still change — sometimes in breaking ways — between releases. There are no stability or
> backwards-compatibility guarantees yet. Expect rough edges, pin a commit if you need stability, and
> see [Contributing](#contributing) if you'd like to help shape it.

## Design philosophy

The recurring principles behind the design — and the bar new features are held to:

- **Local-first.** Project data, evidence, and scans run and stay on your machine by default; nothing
  phones home.
- **Bring your own AI.** The control plane owns the agent loop; models are inference-only and swappable —
  a local model, or any provider you configure. No lock-in to a single vendor.
- **Least-privilege data egress.** What reaches an *external* model is governed per destination by an
  explicit, default-deny clearance model — your data doesn't leave the host unless you've cleared where
  it's going ([ADR-0062](docs/adr/adr-0062-data-egress-trust-boundaries.md)).
- **Evidence before findings.** Tool output becomes an observation with full provenance; only a confirmed
  observation can back a finding — no unaudited leap from a scan to a conclusion.
- **Human-driven.** The Analyst assists the engineer and acts behind an approval policy; it doesn't
  replace judgment or run unsupervised.
- **Composable, not hardcoded.** Security operations are isolated, sandboxed capabilities; checklists,
  templates, and tools are versioned, signed, open-format extensions — extend and share without forking.
- **Open formats.** Projects, checklists, reports, and extensions are portable and inspectable, not
  proprietary blobs.
- **Auditable by construction.** An append-only, hash-chained audit trail records what ran, what was sent
  to a model, and what changed.

## Responsible use

OpenSecBench is a tool for **authorized** security assessment work. Only use it against systems you
own or have **explicit, written permission** to test. Scanning, intercepting traffic against, or
otherwise probing systems without authorization is unlawful in most jurisdictions (e.g. the U.S.
Computer Fraud and Abuse Act, the UK Computer Misuse Act, and equivalents) — and no feature here
changes that.

The tool is built to keep you inside your authorization, not to define it: the engagement record
captures scope + authorization, the scope guard enforces an allowlist, and prohibited-technique
gating and the DLP/egress controls limit what runs and what leaves the host. These are guardrails —
**you** remain responsible for staying within the scope and the law of your engagement.

Provided "as is", without warranty of any kind (see the [LICENSE](LICENSE)). The authors accept no
liability for misuse or for any damage arising from its use.

## What's inside

An IDE-style workbench shell with an always-present AI dock, organized around the assessment lifecycle:

### Assessment

- **Projects, applications, assets, durable targets** — an asset taxonomy with sensitivity levels; a
  durable `target` that carries a knowledge base across engagements; project templates; omni-search
  across everything.
- **Sandboxed capabilities & runners** — every security operation runs in an isolated Docker runner
  with resource limits and scope enforcement. Built-ins: source inventory, SAST (opengrep/Semgrep),
  secret scanning (TruffleHog), SCA (Grype + govulncheck), SBOM (Syft), route mapping, nmap, HTTP
  probe — plus anything shipped as an extension.
- **HTTP toolset** — an intercepting **Proxy** (capture, history, and **traffic rules**: a CEL
  `match → action` engine that can hold, drop, or modify live traffic), **Replay** (edit/send/diff,
  save-as-evidence, scope-guarded), and the **Intercept** queue (edit → forward/drop the traffic a
  hold rule paused).
- **Checklists** — adoptable testing checklists (WSTG, ASVS, or your own); work through them item by
  item, with progress tracked against evidence and auto-checked where a scan or agent can.

### Evidence

- **Evidence loop** — tool output is deterministically interpreted (SARIF, nmap, TruffleHog) into
  **observations** with full provenance; triage and promote to **findings** — only confirmed
  observations can back a finding.
- **Knowledge base** — durable, target-anchored, inherited across engagements; feeds agent context
  and checklist suggestions.

### AI — the Analyst

- **Multi-agent** — least-privilege specialist agents (Lead, Code Analysis, Vuln Validator, Pentester,
  Triage, Report Writer — plus your own custom ones), a **Lead** that delegates, and **playbooks** that
  run as dependency-ordered plans you can trigger, **schedule**, record from a run, or build from
  scratch. Agents read the evidence corpus (code, documents, correspondence, traffic), share a
  workspace, and run sandboxed code — all behind a **trust-curve approval policy** over a fixed scope
  and a per-destination data-egress clearance.

### Reporting

- **Reporting & visualization** — multi-type reports (executive · technical · retest · compliance ·
  branded) in MD/HTML/PDF/DOCX with embedded figures, and a **graph** view of the assessment.

### Governance & provenance

- **Auditable, contained** — an append-only, hash-chained **audit trail**; an encrypted **secrets
  vault** with exec-time injection and output redaction; a **DLP** egress monitor; a **scope guard**
  allowlist; and **per-destination data-egress clearance** on a configurable classification scale
  ([ADR-0062](docs/adr/adr-0062-data-egress-trust-boundaries.md)).

### Extensibility

- **Extensions** — capabilities, checklists, and report templates ship as **signed, digest-pinned**
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
  and swappable: **Anthropic**, **OpenAI-compatible** (Azure OpenAI, DeepSeek, xAI Grok), **gateways**
  (AWS Bedrock, Azure AI Foundry), **local models** (Ollama), and the **Claude CLI** (subscription).
  Tool use is first-class — native tool-use where the backend supports it, a prompted fallback otherwise.

Everything — capabilities, checklists, project templates, playbooks, report templates — is a
versioned, signed, open-format **extension package**.

See [`docs/adr/`](docs/adr/) for architecture decision records and format specs, and
[`docs/TASKS.md`](docs/TASKS.md) / [`docs/TODO.md`](docs/TODO.md) for current work and backlog.

## Development

**Prerequisites:**

- **Go** — the toolchain declared in `go.mod` (auto-installed via `GOTOOLCHAIN`). This alone is enough
  for the headless control plane and the `osb` CLI.
- **Docker** — required at *runtime* to execute the sandboxed capabilities/scanners (opengrep, Grype,
  Syft, TruffleHog, nmap, …). The app runs without it, but scans won't.
- **Node + npm** — to build the React frontend.
- **Desktop app only** — the [Wails](https://wails.io) CLI plus your platform's native webview:
  Linux needs `libgtk-3-dev` + `libwebkit2gtk-4.1-dev`; macOS uses the built-in WebKit (Xcode Command
  Line Tools); Windows uses WebView2 (preinstalled on Windows 11 and with Microsoft Edge).

The full dependency lists are the manifests themselves: Go modules in [`go.mod`](go.mod), frontend
packages in [`frontend/package.json`](frontend/package.json).

```sh
go build ./...      # core packages
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
window. `make dev` and `make build` detect your OS and pass the right webview build tags
automatically — Linux, macOS, and Windows all work:

```sh
make dev      # run the desktop app with live reload
make build    # package a desktop binary into ./build/bin
```

Wails builds for the host OS (it does not cross-compile), so run these on the machine you want the
binary for. The Analyst is configured from the app's settings (see below) — no environment variables
needed.

- No `make` on Windows? Run `wails dev -tags desktop` / `wails build -tags desktop` directly.
- On older Linux distros that ship webkit2gtk-4.0 instead of 4.1, run `make dev WAILS_TAGS=desktop`.

## The Analyst (AI)

The control plane owns the agent loop; providers are inference-only (ADR-0006/0017). Configure a
**provider connection** once in the app's **Analyst settings** (ADR-0052): pick a type, add a
credential (kept in the encrypted vault, never in the environment), let it **discover the available
models** live, and activate it. The connection is persisted and reused across runs — there's nothing
to pass on the command line each time.

Supported provider types:

- **Anthropic** — API key.
- **OpenAI-compatible** — any OpenAI-style endpoint (DeepSeek, xAI Grok, Azure OpenAI, …); set the base URL.
- **Gateways** — **AWS Bedrock** and **Azure AI Foundry**, where one connection serves many models across families.
- **Ollama** — local, no key, no egress.
- **Claude via the CLI** — your local Claude subscription login (optionally sandboxed).

**Data-egress trust boundaries.** What the Analyst may send to an *external* model is decided per
destination: each connection — and each model it serves — carries a **data clearance** on a configurable
classification scale (Library ▸ Data classification), and project content only leaves the host if the
destination is cleared for its tier. The gate is **default-deny** (anything not explicitly cleared is
withheld), a *restricted* engagement can only tighten it, vault secrets and canaries are always redacted,
and a **local** model is never gated. Full boundary map: [ADR-0062](docs/adr/adr-0062-data-egress-trust-boundaries.md).

The connection, its discovered models, the trust-curve approval policy, and custom agents are all
managed from the Analyst settings; playbooks are triggered, scheduled, and built from the **Agents**
surface. The Analyst calls read tools over your data (auto-approved) and gates outbound/mutating
actions through an **approval queue** (`osb approval list|approve|deny`):

```sh
osb analyst ask "how many findings are there, and which are high severity?"
```

**Headless / no UI?** When you run `cmd/daemon` without the desktop app (CI, scripts, a quick
try-out), `OSB_LLM_*` still bootstraps a provider at startup as a fallback:

```sh
# local Ollama, no key, no egress (MODEL must be one you've pulled):
OSB_LLM_PROVIDER=ollama OSB_LLM_MODEL=qwen2.5 go run ./cmd/daemon
# Anthropic API:
OSB_LLM_PROVIDER=anthropic OSB_LLM_API_KEY=... go run ./cmd/daemon
```

Governance & agent env (dev convenience; the desktop UI exposes the same controls):

- `OSB_AGENT_MAX_TOKENS` — per-turn token budget (default 200000); the run stops if exceeded.
- `OSB_AGENT_MAX_CONCURRENT` — cap on concurrent sub-agents (default 4).
- `OSB_LLM_NATIVE_TOOLS` — native tool-use is on by default for capable backends; set `0` to force the
  prompted fallback.
- `OSB_LLM_CLI_SANDBOX=1` (+ `OSB_LLM_CLI_IMAGE`) — run `claude-cli` inside a runner container that mounts
  only `~/.claude/.credentials.json`, instead of on the host (build the image with `make claude-image`).
- `OSB_WORKSPACE_DIR` — root for the per-project agent workspace (defaults beside the database).

## Contributing

Contributions are welcome — issues, discussion, and pull requests all help. OpenSecBench is built
by and for AppSec practitioners, so real-world workflow feedback is as valuable as code.

Good places to start:

- **Report a bug or request a feature** — open an issue with enough detail to reproduce or motivate it.
- **Read the design first** — the [`docs/adr/`](docs/adr/) ADRs explain why things are the way they are;
  aligning a change with the relevant ADR (or proposing a new one) makes review much faster.
- **Extensions** — capabilities, checklists, and report templates ship as signed, open-format
  packages, so many additions don't require touching the core (see the extension-format ADRs).

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the full workflow and the checks a PR needs to pass.
Found a security issue in OpenSecBench itself? Please report it privately — see
[`SECURITY.md`](SECURITY.md).

## License

Licensed under the [Apache License 2.0](LICENSE).
