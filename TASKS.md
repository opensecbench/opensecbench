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

## P5 — Playbooks (tactics)

- [x] Playbook definitions (pkg/playbook): sequential capability steps
- [x] Playbook runner engine: run steps against an asset, record playbook_run + task IDs
- [x] Schema (0007) + repos for playbook_runs
- [x] API + osb CLI: playbook list/run, playbook-run list/get
- [x] Analyst tools: list_playbooks / run_playbook (gated)
- [x] Playbooks tab in the frontend
- [ ] Playbook editor (author/edit task graphs) — later
- [ ] Conditional steps + per-step approval gates — later

## P6 — Scope guard

- [x] Scope allowlist model + guard logic (pkg/scope): host / domain / cidr matching
- [x] Schema (0008) + repos for scope_entries
- [x] Network capability (http-probe) with Manifest.TargetParam
- [x] Engine enforcement: scope.Check before network capabilities; blocked = failed task (audited)
- [x] Scope management API + osb CLI (scope add/list/delete); capability run --project
- [ ] Scope enforcement for interactive sessions / proxy targets — P7

## P7 — Proxy + Repeater + Terminal

- [x] ADR-0007 (HTTP capture, Repeater & interactive sessions)
- [x] http_exchange model + migration (0009) + store repos
- [x] Repeater transport (pkg/repeater): send one request, capture response, no redirect-follow, body cap
- [x] Scope-guarded send in the API (reuses pkg/scope); out-of-scope refused before sending
- [x] Save-as-evidence: response → CAS artifact + human-origin observation (ADR-0005)
- [x] API + osb CLI: repeater send/list/get/evidence
- [x] Repeater tab in the frontend (send, response view, save-as-evidence, history)
- [ ] Intercepting proxy (CA/TLS capture) → http_exchange rows with origin=proxy
- [x] Interactive terminal: shell in a sandboxed container over a PTY (pkg/session)
- [x] WebSocket terminal API + xterm.js tab; transcript captured to CAS on close, save-as-evidence
- [x] Preconfigured throwaway browser: `osb proxy browser` launches Chromium pointed at the proxy
      and trusting only the CA via --ignore-certificate-errors-spki-list (no system trust change)
- [ ] SSH/PTY to an external host (scoped); agent co-drive through the approval gate
- [ ] Fragment-level response selection as evidence (byte-range)

## Audit trail (cross-cutting, wired during P7)

- [x] Persisted hash-chained audit_events (migration 0011) + store repos; chain resumes across restarts
- [x] Record governed actions: task run/blocked, scope add/delete, repeater send/blocked,
      session open/close, evidence promotions, playbook run, approval decisions, Analyst tool calls
- [x] GET /v1/audit + client + `osb audit` CLI + Audit tab in the workbench
- [ ] Broaden coverage to entity CRUD; `osb audit --verify` chain check; decide fail-closed policy

## P8 — Reporting + visualizations + coverage

- [x] ADR-0008 (reporting & visualization)
- [x] Report engine (pkg/report): gathered Data snapshot, confirmed-evidence-only rule in one Builder
- [x] Built-in templates: executive + technical → Markdown + HTML (self-contained, escaped)
- [x] Coverage roll-up (apps/assets/tasks/capabilities) + severity summary in Data
- [x] Report persistence (migration 0012) + generate API + client + `osb report` CLI
- [x] Inline-SVG severity chart (pkg/viz) embedded in HTML reports (CSP-safe, no JS)
- [x] PDF via headless Chromium (pkg/browser shared with proxy); degrades if no browser
- [x] Reports tab in the workbench (template + format, generate, open)
- [x] Retest report type (findings grouped by remediation status)
- [x] Compliance mapping report (findings grouped by CWE)
- [ ] More report types: client-branded
- [x] Coverage heatmap viz (severity × remediation status) embedded in the technical report
- [ ] More visualizations: dependency/API maps, topology (interactive workbench views)
- [ ] Methodology coverage model + roll-up (needs a methodology/checklist entity)
- [x] Notifications: in-app feed + bell + OS-native CLI watch (approval waiting, report ready)
- [ ] Report templates + visualizations as installable extension packages
