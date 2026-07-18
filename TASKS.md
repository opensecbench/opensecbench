# Task checklist

Living checklist of work in progress. Check items off as they complete. Future / deferred work
lives in [TODO.md](TODO.md). See the approved plan for the full P0–P12 roadmap.

## P0 — Foundation

- [ ] Monorepo scaffold (go.mod, layout, README, .gitignore, tracking files)
- [ ] Architecture docs / ADRs for the major subsystems (docs-first)
- [ ] GitHub Actions CI (build, vet, test, lint, secret scan)
- [ ] Control-plane skeleton: `cmd/daemon` + `/healthz` (pkg/api)
- [ ] SQLite bootstrap + migrations runner (pkg/store)
- [ ] Content-addressed storage skeleton (pkg/cas)
- [ ] Append-only audit writer (pkg/audit)
- [ ] Global Home + Workbench shells (frontend, later in P0)
- [ ] Wails desktop boots the control plane in-process (later in P0)
