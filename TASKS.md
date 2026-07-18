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
- [x] Methodology / Analyst (agent) surfaces — delivered in later phases: Analyst panel (P4),
      Methodology tab (P8/ADR-0009); Workbench now spans the full lifecycle

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
- [x] Scope enforcement for proxy targets (host allowlist gate); sessions are local sandboxes

## P7 — Proxy + Repeater + Terminal

- [x] ADR-0007 (HTTP capture, Repeater & interactive sessions)
- [x] http_exchange model + migration (0009) + store repos
- [x] Repeater transport (pkg/repeater): send one request, capture response, no redirect-follow, body cap
- [x] Scope-guarded send in the API (reuses pkg/scope); out-of-scope refused before sending
- [x] Save-as-evidence: response → CAS artifact + human-origin observation (ADR-0005)
- [x] API + osb CLI: repeater send/list/get/evidence
- [x] Repeater tab in the frontend (send, response view, save-as-evidence, history)
- [x] Intercepting proxy (CA/TLS capture) → http_exchange rows with origin=proxy
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
- [x] `osb audit --verify` chain check (tamper detection)
- [ ] Broaden audit coverage to entity CRUD; decide fail-closed-on-audit-failure policy

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
- [x] Client-branded report type
- [x] Coverage heatmap viz (severity × remediation status) embedded in the technical report
- [x] Interactive graph tab: project structure + traffic/endpoint map (pan/zoom/hover)
- [ ] More graph kinds: dependency (SCA) + topology (nmap) — pending those capabilities
- [x] Methodology coverage model + roll-up (ADR-0009): catalog packs, adoption, item status,
      coverage % on the Workbench and in reports
- [x] Notifications: in-app feed + bell + OS-native CLI watch (approval waiting, report ready)
- [x] Report templates as installable extension packs (extension.json `reports`)
- [ ] Visualization definitions as extension packs (graph kinds); pending a viz pack schema

## P9 — Knowledge base

- [x] ADR-0010 (knowledge base)
- [x] KB entries anchored to durable targets (migration 0015) + model + store
- [x] Project inheritance: a project's KB = union across the targets it references
- [x] Provenance: agent drafts (origin=thread) unreviewed; human entries confirmed; review gate
- [x] API + osb CLI (kb list/add/review)
- [x] Analyst draft_kb_entry + list_targets tools; KB in omni-search
- [x] Workbench Knowledge tab (add, AI-draft marking, confirm/reject)
- [ ] Broader scope resolution (group/org/global-personal)
- [x] KB-driven methodology applicability (KB keywords → adopt suggestions)
- [ ] KB entry versioning; link entries to source evidence/findings
- [ ] KB packs as extension packages

## P10 — Secrets + integrations

- [x] ADR-0011 (secrets vault, DLP & redaction)
- [x] Encrypted vault (pkg/secret, AES-256-GCM): references-not-values, key from env or 0600 file
- [x] Secret API/CLI (never returns plaintext) + exec-time injection into runners + output redaction
- [x] DLP monitor (pkg/dlp): scan LLM egress for vault secrets/canaries/patterns; block external, alert
- [x] Canaries (exfil tripwires) + dlp_events trail + API/CLI
- [x] Integrations (pkg/integration): Jira + DefectDojo finding push, credentials from vault,
      idempotent external_links; API + `osb finding push/links`
- [ ] Integration pull (DefectDojo findings/scans in) + DependencyTrack SBOM
- [ ] Jira/resource watchers (schedule → notify/create task/run playbook)
- [ ] Integrations modeled as first-class capabilities (gate/audit via the capability contract)
- [x] policy_profile entity (personal/corporate/strict; governs Analyst egress)
- [ ] OS keychain / KMS vault key custody

## P11 — Collaboration (export/import first)

- [x] ADR-0012 (collaboration: portable export/import)
- [x] pkg/bundle: encrypted (scrypt+AES-256-GCM) project bundle — findings + evidence + KB + blobs
- [x] Export gathers the shareable graph + CAS blobs; Import remaps IDs (re-import safe), preserves
      evidence content hashes
- [x] API (X-OSB-Passphrase header) + osb CLI (project export/import); verified across two daemons
- [x] Publisher-key signing of bundles (ed25519 sidecar; `export --sign-key` / `import --verify`)
- [x] Mediated sharing: DefectDojo finding push (P10) + Slack/Teams webhook notifications
- [ ] Subset export (findings-only) + full-backup mode (include tasks/audit)
- [ ] Hosted team service (deferred until existing-platform sharing proves insufficient)

## Extension loader (P2 core, delivered later; ADR-0013)

- [x] ADR-0013 (extension loader: format, ed25519 signing, trust store, digest pinning)
- [x] pkg/extension: directory packages, container capabilities + methodology packs, sign/verify/trust
- [x] Control plane loads <data>/extensions at startup into the capability + methodology registries
- [x] First-party example pack (extensions/trufflehog) + extensions/README
- [x] API GET /v1/extensions + osb ext list/keygen/sign; verified e2e (sign→trust→load→cap registered)
- [x] Report extension pack type
- [ ] Playbook/visualization extension pack types; manifest `permissions` schema
- [ ] Runtime install/reload (drop-in without restart) + trust management over the API
- [ ] Interpret trufflehog JSON output into observations (currently captured as a raw artifact)

## P12 — Community extension hub (ADR-0014)

- [x] ADR-0014 (community hub: static signed index, publish/browse/install, explicit trust)
- [x] pkg/hub: Index/PackageEntry, ArchiveDir/Extract (traversal-safe), Publish, FetchIndex,
      DownloadArchive (transit digest verify)
- [x] Concurrency-safe capability + methodology registries (runtime extension registration)
- [x] Control plane: GET /v1/hub/index, POST /v1/hub/install (verify→extract→hot-register),
      POST /v1/extensions/trust; explicit trust-on-install
- [x] osb hub browse/install/publish + ext trust flow; e2e (publish→serve→install→live capability)
- [ ] Hosted hub service (accounts, uploads, submission scanning, reputation, moderation/takedown)
- [x] Frontend Extensions & Governance view (browse/install/trust + policy)
- [ ] Version constraints / update flow; uninstall
