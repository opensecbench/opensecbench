# Task checklist

Living checklist of work in progress. Check items off as they complete. Future / deferred work
lives in [TODO.md](TODO.md). See the approved plan for the full P0–P12 roadmap.

## P0 — Foundation

- [x] Monorepo scaffold (go.mod, layout, README, .gitignore, tracking files)
- [x] Architecture docs / ADRs for the major subsystems (docs-first)
- [x] GitHub Actions CI (build, vet, test, lint, secret scan)
- [x] Control-plane skeleton: `cmd/daemon` + `/healthz` (pkg/api)
- [x] Migrations loader + embedded migrations (pkg/store); DB open wired in P1
- [x] Content-addressed storage skeleton (pkg/cas)
- [x] Append-only, hash-chained audit writer (pkg/audit)
- [x] Reusable control-plane bootstrap (pkg/controlplane) + CORS for browser/Wails frontends
- [x] React (Vite + TS) frontend: Global Home (projects, template create, omni-search)
- [x] Wails desktop boots the control plane in-process (verified locally via `wails dev`)
- [x] Project Workbench: Applications & Assets, Context (upload), Scan (run → triage → finding), Findings
- [ ] Methodology / Analyst (agent) surfaces — later phases

## P1 — Targets, projects, assets, templates, search

- [x] SQLite driver (modernc.org/sqlite) + migration applier (pkg/store)
- [x] Core hierarchy schema: organizations, groups, targets, projects, applications, assets (0002)
- [x] Domain models (pkg/model) + repositories (organizations, targets, projects)
- [x] Daemon opens DB + applies migrations on startup; `/readyz` reports DB status
- [x] Project + organization + target CRUD over the HTTP API (v1)
- [x] Application + asset repositories + endpoints (sensitivity default-from-location); asset-targeted runs
- [x] Project templates / archetypes (scaffold project + default application)
- [x] Omni-search v1 (projects · applications · assets · findings · observations)
- [x] Context ingest (docs/emails/chats/notes → CAS input artifacts, searchable)
- [x] `osb` CLI + pkg/client over the API (health, project list/create/get/delete)

## P2 — Capability & runner core

- [x] ADR-0004 (runner protocol & sandboxing)
- [x] tasks + artifacts schema (0003) + repositories (provenance chain)
- [x] Sandboxed Docker LocalRunner (pkg/runner) with limits + read-only mounts
- [x] Capability contract + registry (pkg/capability); built-ins: source-inventory, semgrep
- [x] Task engine (pkg/task): capability → sandbox → CAS artifact → provenance
- [x] API + osb CLI: capabilities list, task run/get, artifact content
- [ ] Resolve target dir from an asset (needs asset endpoints); async task scheduling
- [x] Semgrep verified against a real repo (offline, local rule; --config auto needs egress)

## P3 — Evidence loop (SARIF → observations → findings)

- [x] ADR-0005 (evidence interpretation + finding lifecycle)
- [x] observations + findings schema (0004) + repos; only confirmed obs can back a finding
- [x] SARIF interpreter (pkg/interpret) + engine auto-interpret on SARIF output
- [x] Triage API + osb CLI: observation list/review, finding create/list/get
- [x] Verified end-to-end: semgrep → observation → confirm → finding (via CLI)
- [x] Observation/finding views in the frontend (Scan + Findings tabs)
- [ ] LLM interpreter (origin=thread) — P4+
- [ ] Fragment-level evidence tagging (ADR-0002 evidence entity) — later

## P4 — Analyst (agent runtime + providers)

- [x] ADR-0006 (agent runtime & providers)
- [x] Provider abstraction (pkg/llm): Mock, Claude CLI, OpenAI-compat (Ollama/DeepSeek/Grok), Anthropic
- [x] Agent loop (pkg/agent): structured tool-calling + approval gate + audit + step cap
- [x] Analyst service: read-only tools over the store; POST /v1/analyst/ask; osb analyst ask
- [x] Provider configured via OSB_LLM_* (ollama/deepseek/grok/claude-cli/anthropic)
- [x] Gated capability-execution tools (agent runs scans behind approval)
- [x] Async approval queue + resumable agent runs (Session Advance/Resume)
- [x] Threads + fork persistence (schema + store + fork)
- [x] Budgets (token) + data-egress policy by sensitivity
- [x] Analyst panel in the frontend (threads, chat, approve/deny)
- [ ] Native tool-use for backends that support it (reliability on small models)
- [ ] Concurrent-agent cap; usage/$ tracking surfaced on Home
