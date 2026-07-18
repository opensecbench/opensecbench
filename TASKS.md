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
- [x] React (Vite + TS) frontend: Global Home (projects list/create/delete) against the API
- [ ] Wails desktop boots the control plane in-process (glue added; run locally with webkit)
- [ ] Full Workbench surfaces (methodology/evidence/findings/Analyst) — later phases

## P1 — Targets, projects, assets, templates, search

- [x] SQLite driver (modernc.org/sqlite) + migration applier (pkg/store)
- [x] Core hierarchy schema: organizations, groups, targets, projects, applications, assets (0002)
- [x] Domain models (pkg/model) + repositories (organizations, targets, projects)
- [x] Daemon opens DB + applies migrations on startup; `/readyz` reports DB status
- [x] Project + organization + target CRUD over the HTTP API (v1)
- [x] Application + asset repositories + endpoints (sensitivity default-from-location); asset-targeted runs
- [x] Project templates / archetypes (scaffold project + default application)
- [x] Omni-search v1 (projects · applications · assets · findings · observations)
- [ ] Context ingest (docs/emails/chats into CAS) — search extends to it later
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
- [ ] Observation/finding views in the frontend
- [ ] LLM interpreter (origin=thread) — P4
- [ ] Fragment-level evidence tagging (ADR-0002 evidence entity) — later
