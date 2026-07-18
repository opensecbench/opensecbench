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
- [ ] Application + asset repositories + endpoints (incl. sensitivity default-from-location)
- [ ] Project templates / archetypes
- [ ] Context ingest + omni-search v1
- [x] `osb` CLI + pkg/client over the API (health, project list/create/get/delete)
