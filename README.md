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

Requires Go 1.22+. (Docker and Wails are needed for later phases.)

```sh
go build ./...
go test ./...
```
