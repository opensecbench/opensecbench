# OpenSecBench

An open, free, local-first **security assessment workbench** — a unified environment for
planning and conducting application security assessments: executable methodologies, distributed
tool runners, evidence management with full provenance, and an AI assistant (the **Analyst**),
across the full lifecycle of an assessment.

OpenSecBench is not a scanner aggregator, a vulnerability-management system, or an autonomous
pentester. It is a working environment that makes skilled AppSec and assessment engineers
dramatically more effective — and that a community can extend and share.

> Status: early development (phase P0 — foundation). Repository is private during the build and
> will be open-sourced at release.

## License

Licensed under the [Apache License 2.0](LICENSE).

## Architecture (at a glance)

- **Go control plane** — a standalone headless daemon exposing a local HTTP API. All clients
  (desktop, CLI, future web) are thin clients against it; domain logic never depends on the
  desktop framework.
- **Wails + React desktop** — the primary client; boots the control plane in-process.
- **SQLite** for structured project data; **content-addressed storage** for immutable artifacts.
- **OCI-sandboxed capabilities** — every security operation runs in an isolated runner.
- **Own agent runtime** — provider-agnostic (Anthropic, AWS Bedrock, Azure/OpenAI, Vertex/Gemini,
  local models); the Analyst proposes and (gated) executes the same capabilities a human uses.

Everything — capabilities, methodologies, project templates, playbooks, visualizations, report
templates — is a versioned, signed, open-format **extension package**.

See [`docs/`](docs/) for architecture decision records and format specifications, and
[`TASKS.md`](TASKS.md) / [`TODO.md`](TODO.md) for current work and backlog.

## Repository layout

```
cmd/        entrypoints: desktop, daemon, osb (CLI), runner, mcp
pkg/        control-plane packages (see docs/adr-0001-architecture-overview.md)
extensions/ first-party packages (same format as third-party)
frontend/   React + TypeScript desktop UI
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
go run ./cmd/daemon                # in one terminal
cd frontend && npm install && npm run dev   # in another → http://localhost:5173
```

**Desktop app (Wails):** boots the control plane in-process and renders the frontend in a native
window. Needs the [Wails](https://wails.io) CLI and, on Linux, the GTK/WebKit dev libraries
(`libgtk-3-dev`, `libwebkit2gtk-4.1-dev`). The desktop entrypoint (`main.go`) is behind the
`desktop` build tag so it never affects `go build ./...` or CI — **you must pass the tag to Wails**
(the Makefile does it for you):

```sh
make dev                       # = wails dev -tags "desktop webkit2_41"
make build                     # = wails build -tags "desktop webkit2_41"
OSB_LLM_PROVIDER=claude-cli make dev   # with the Analyst enabled
```

Notes:
- Without the `desktop` tag, Wails builds a stub that prints a hint and exits.
- On Ubuntu/Pop!_OS **24.04+**, webkit is 4.1, so the `webkit2_41` tag is required (the Makefile
  includes it). On older distros with webkit2gtk-4.0, use `make dev WAILS_TAGS=desktop`.

**The Analyst (AI):** the control plane owns the agent loop; providers are inference-only
(ADR-0006). Configure one via `OSB_LLM_*` when starting the daemon (keys come from the vault in
production — env is a dev convenience):

```sh
# local Ollama, no key, no egress (MODEL must be one you have pulled):
OSB_LLM_PROVIDER=ollama OSB_LLM_MODEL=qwen2.5 go run ./cmd/daemon
# remote/non-default Ollama — set the OpenAI-compatible base URL (note the /v1):
OSB_LLM_PROVIDER=ollama OSB_LLM_BASE_URL=http://10.0.0.5:11434/v1 OSB_LLM_MODEL=qwen2.5 go run ./cmd/daemon
# hosted, OpenAI-compatible:
OSB_LLM_PROVIDER=deepseek OSB_LLM_API_KEY=... go run ./cmd/daemon
OSB_LLM_PROVIDER=grok     OSB_LLM_API_KEY=... go run ./cmd/daemon
# Claude via the CLI (one-shot inference):
OSB_LLM_PROVIDER=claude-cli go run ./cmd/daemon
# Anthropic API:
OSB_LLM_PROVIDER=anthropic OSB_LLM_API_KEY=... go run ./cmd/daemon

osb analyst ask "how many findings are there, and which are high severity?"
```

Providers: `ollama`, `deepseek`, `grok`, `openai`/`azure` (set `OSB_LLM_BASE_URL`), `anthropic`,
`claude-cli`. The Analyst calls read-only tools over your data (auto-approved) and can run
capabilities behind an **approval queue** (`osb approval list|approve|deny`).

Governance (env, dev convenience):

- `OSB_EGRESS_POLICY` — `strict` (default) blocks running a capability on a **private** asset when
  the provider is external (e.g. DeepSeek/Grok/Anthropic); `open` allows it. Local providers
  (Ollama) are never blocked.
- `OSB_AGENT_MAX_TOKENS` — per-turn token budget (default 200000); the run stops if exceeded.

