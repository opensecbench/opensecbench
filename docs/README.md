# OpenSecBench design docs

Architecture Decision Records (ADRs) and open-format specifications. Per our working agreement,
**a short design doc lands here before its subsystem is built**, and docs are kept current as
decisions change. The authoritative product/roadmap plan lives outside the repo (planning
scratch); these docs are the buildable, versioned foundation.

## ADRs

| # | Title | Status |
|---|-------|--------|
| [0001](adr-0001-architecture-overview.md) | Architecture overview & boundaries | Accepted |
| [0002](adr-0002-data-model-and-provenance.md) | Data model & provenance | Accepted |
| [0003](adr-0003-capability-and-extension-format.md) | Capability & extension package format | Accepted |
| [0004](adr-0004-runner-protocol.md) | Runner protocol & sandboxing | Accepted |
| [0005](adr-0005-evidence-interpretation.md) | Evidence interpretation & finding lifecycle | Accepted |
| [0006](adr-0006-agent-runtime-and-providers.md) | Agent runtime & provider layer | Accepted |
| [0007](adr-0007-http-repeater-and-sessions.md) | HTTP capture, Repeater & interactive sessions | Accepted (Repeater + Terminal + Proxy); SSH & agent co-drive staged |
| [0008](adr-0008-reporting-and-visualization.md) | Reporting & visualization | Accepted (exec+technical, MD/HTML/PDF, SVG); more types staged |
| [0009](adr-0009-methodology-and-coverage.md) | Methodology & coverage | Accepted (packs + adoption + coverage); applicability automation staged |
| [0010](adr-0010-knowledge-base.md) | Knowledge base | Accepted (target-anchored, inheritance, drafting, search); broader scope staged |
| [0011](adr-0011-secrets-dlp-redaction.md) | Secrets vault, DLP & redaction | Accepted (vault, injection, redaction, DLP+canaries); KMS staged |
| [0012](adr-0012-collaboration-export-import.md) | Collaboration: portable export/import | Accepted (encrypted bundle); mediated sharing + signing staged |
| [0013](adr-0013-extension-loader.md) | Extension loader | Accepted (dir packages, container caps + methodology, ed25519 signing); more pack types staged |
| [0014](adr-0014-community-hub.md) | Community extension hub | Accepted (static signed index, publish/browse/install, explicit trust); scanning/reputation staged |
| [0015](adr-0015-workbench-ide-shell.md) | Workbench IDE shell | Accepted (Phases 1–3 delivered: frame reshape; explorer + methodology landing + coverage; persistent multi-document keep-alive + in-context Replay↔item evidence binding) |
| [0016](adr-0016-http-traffic-toolset.md) | HTTP traffic toolset (Proxy · Replay · Intercept) | Accepted — fully delivered (rename + Steps 1–4) |
| [0017](adr-0017-first-class-tool-use.md) | First-class tool use & provider translation | Accepted — fully delivered (Phases 1–5: typed schema, tool-aware providers, canonical persistence, native adapters + conformance, expanded governed toolset). Native on by default; evolves ADR-0006 |
| [0018](adr-0018-sandboxed-cli-provider.md) | Sandboxed claude-cli inference provider | Accepted — delivered (credential-only mount, egress network, runner stdin). Opt-in; extends ADR-0006, composes ADR-0004/0011 |
| [0019](adr-0019-agent-profiles-orchestration.md) | Agent profiles & orchestration | Accepted — fully delivered. Least-privilege profiles + trust-curve approval + delegation/Lead + adaptable playbooks (plan DAG runner) + record-as-playbook/builder + scheduled runs + triage→report + user-defined profiles/playbooks; workbench UI. Sits on ADR-0020 |
| [0020](adr-0020-agent-workspace-corpus.md) | Agent workspace & corpus investigation | Accepted — delivered. The capability layer: source reads (`read_file`/`list_dir`/`grep_code`/`find_files`) + corpus reads (`read_context`/`list_context`/`get_kb_entry`) + workspace (`workspace_*`) + sandboxed `run_code`, all governed (project scope, path confinement, DLP, gating) |
| [0021](adr-0021-settings-and-model-catalog.md) | Settings, model catalog & appearance | Accepted — delivered. Sectioned Settings surface + section registry (core + extension-declared, §6); curated model catalog + fixed-vocab tags + cross-provider routing; light/dark + accent theming |
| [0022](adr-0022-async-task-execution.md) | Asynchronous capability execution | Accepted — delivered. Enqueue + bounded worker pool (`OSB_TASK_WORKERS`, default 3); `POST /v1/tasks` → 202 pending, client polls; runs survive disconnect; queued-task cancel + crash reconciliation. Playbooks async too (`Start` → 202 run); agent plans already background — async everywhere |
| [0023](adr-0023-durable-task-queue.md) | Durable task queue | Accepted — delivered. The `tasks` row IS the queue: atomic DB claim (`ClaimNextPendingTask`) + notify/poll workers; pending work survives restart, interrupted runs resume (at-least-once, `OSB_TASK_MAX_ATTEMPTS` cap); persists secret-ref names + target_dir for reconstruction. Supersedes 0022's in-process-queue limitation |
| [0024](adr-0024-remote-runner.md) | Remote runner (outbound-connect) | Accepted — Phase 1 delivered. A runner agent (`osb-runner`) dials home, ed25519-enrolled; `pkg/runnerhub` broker + separate `--runner-addr` listener; network capabilities run from the runner's vantage; scope enforced control-plane-side before dispatch. Phase 2 (Replay/proxy egress via runner) deferred |
| [0025](adr-0025-egress-via-runner.md) | Egress via runner (Replay) | Accepted — Phase 2a delivered. Replay send (UI + Analyst `send_request` tool) can egress from a chosen runner's vantage via an HTTP-request job on the runner protocol; shared `egressSend` selector; `egress` provenance on the exchange; scope stays control-plane-side. Proxy egress (2b) deferred |

Later subsystems get an ADR when their phase begins. See [`../TODO.md`](../TODO.md).

## Conventions

- One ADR per decision; format: **Context → Decision → Consequences**.
- ADRs are append-only in spirit: supersede rather than silently rewrite; note status changes.
- Format specs (capability manifest, extension package, playbook, project template) are versioned
  JSON Schemas under `spec/` once they stabilize; ADR-0003 defines their shape.
